/*
 * Copyright © 2015-2018 Aeneas Rekkas <aeneas+oss@aeneas.io>
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * @author		Aeneas Rekkas <aeneas+oss@aeneas.io>
 * @copyright 	2015-2018 Aeneas Rekkas <aeneas+oss@aeneas.io>
 * @license 	Apache-2.0
 *
 */

package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goauth "golang.org/x/oauth2"

	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	"github.com/ory/fosite/handler/oauth2"
)

func TestAuthorizeCodeFlow(t *testing.T) {
	for _, strategy := range []oauth2.AccessTokenStrategy{
		hmacStrategy,
	} {
		runAuthorizeCodeGrantTest(t, strategy)
	}
}

func TestAuthorizeCodeFlowDupeCode(t *testing.T) {
	for _, strategy := range []oauth2.AccessTokenStrategy{
		hmacStrategy,
	} {
		runAuthorizeCodeGrantDupeCodeTest(t, strategy)
	}
}

func runAuthorizeCodeGrantTest(t *testing.T, strategy interface{}) {
	f := compose.Compose(new(fosite.Config), fositeStore, strategy, compose.OAuth2AuthorizeExplicitFactory, compose.OAuth2TokenIntrospectionFactory)
	ts := mockServer(t, f, &fosite.DefaultSession{Subject: "foo-sub"})
	defer ts.Close()

	oauthClient := newOAuth2Client(ts)
	fositeStore.Clients["my-client"].(*fosite.DefaultClient).RedirectURIs[0] = ts.URL + "/callback"

	var state string
	for k, c := range []struct {
		description    string
		setup          func()
		check          func(t *testing.T, r *http.Response)
		params         []goauth.AuthCodeOption
		authStatusCode int
	}{
		{
			description: "should fail because of audience",
			params:      []goauth.AuthCodeOption{goauth.SetAuthURLParam("audience", "https://www.ory.sh/not-api")},
			setup: func() {
				oauthClient = newOAuth2Client(ts)
				state = "12345678901234567890"
			},
			authStatusCode: http.StatusNotAcceptable,
		},
		{
			description: "should fail because of scope",
			params:      []goauth.AuthCodeOption{},
			setup: func() {
				oauthClient = newOAuth2Client(ts)
				oauthClient.Scopes = []string{"not-exist"}
				state = "12345678901234567890"
			},
			authStatusCode: http.StatusNotAcceptable,
		},
		{
			description: "should pass with proper audience",
			params:      []goauth.AuthCodeOption{goauth.SetAuthURLParam("audience", "https://www.ory.sh/api")},
			setup: func() {
				oauthClient = newOAuth2Client(ts)
				state = "12345678901234567890"
			},
			check: func(t *testing.T, r *http.Response) {
				var b fosite.AccessRequest
				b.Client = new(fosite.DefaultClient)
				b.Session = new(defaultSession)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&b))
				assert.EqualValues(t, fosite.Arguments{"https://www.ory.sh/api"}, b.RequestedAudience)
				assert.EqualValues(t, fosite.Arguments{"https://www.ory.sh/api"}, b.GrantedAudience)
				assert.EqualValues(t, "foo-sub", b.Session.(*defaultSession).Subject)
			},
			authStatusCode: http.StatusOK,
		},
		{
			description: "should pass",
			setup: func() {
				oauthClient = newOAuth2Client(ts)
				state = "12345678901234567890"
			},
			authStatusCode: http.StatusOK,
		},
	} {
		t.Run(fmt.Sprintf("case=%d/description=%s", k, c.description), func(t *testing.T) {
			c.setup()

			resp, err := http.Get(oauthClient.AuthCodeURL(state, c.params...))
			require.NoError(t, err)
			require.Equal(t, c.authStatusCode, resp.StatusCode)

			if resp.StatusCode == http.StatusOK {
				token, err := oauthClient.Exchange(goauth.NoContext, resp.Request.URL.Query().Get("code"))
				require.NoError(t, err)
				require.NotEmpty(t, token.AccessToken)

				httpClient := oauthClient.Client(goauth.NoContext, token)
				resp, err := httpClient.Get(ts.URL + "/info")
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, resp.StatusCode)

				if c.check != nil {
					c.check(t, resp)
				}
			}
		})
	}
}

func runAuthorizeCodeGrantDupeCodeTest(t *testing.T, strategy interface{}) {
	f := compose.Compose(new(fosite.Config), fositeStore, strategy, compose.OAuth2AuthorizeExplicitFactory, compose.OAuth2TokenIntrospectionFactory)
	ts := mockServer(t, f, &fosite.DefaultSession{})
	defer ts.Close()

	oauthClient := newOAuth2Client(ts)
	fositeStore.Clients["my-client"].(*fosite.DefaultClient).RedirectURIs[0] = ts.URL + "/callback"

	oauthClient = newOAuth2Client(ts)
	state := "12345678901234567890"

	resp, err := http.Get(oauthClient.AuthCodeURL(state))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	token, err := oauthClient.Exchange(goauth.NoContext, resp.Request.URL.Query().Get("code"))
	require.NoError(t, err)
	require.NotEmpty(t, token.AccessToken)

	req, err := http.NewRequest("GET", ts.URL+"/info", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	_, err = oauthClient.Exchange(goauth.NoContext, resp.Request.URL.Query().Get("code"))
	require.Error(t, err)

	resp, err = http.DefaultClient.Get(ts.URL + "/info")
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
