// Copyright 2026, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package plunge

// The acceptance test of INTENTIUS/plunge#35: a plunge+azblob:// URL with a localhost endpoint makes no request to a
// host that is not local, and never signs in with the shell's Azure login.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loopbackClient is an http.Client whose transport fails the test on any host that is not the loopback address.
type loopbackClient struct {
	t    *testing.T
	next http.RoundTripper
}

func (c loopbackClient) Do(r *http.Request) (*http.Response, error) {
	host := r.URL.Hostname()
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		c.t.Errorf("a request went to the non-local host %s", r.URL.Host)
		return nil, fmt.Errorf("test: %s is not a loopback host", r.URL.Host)
	}
	return c.next.RoundTrip(r)
}

// recorder is a fake Azure: it records each request and answers 404, except where answer says otherwise.
type recorder struct {
	mu     sync.Mutex
	seen   []string
	auth   []string
	answer func(w http.ResponseWriter, r *http.Request) bool
}

func (rec *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec.mu.Lock()
	rec.seen = append(rec.seen, r.Method+" "+r.URL.Path)
	rec.auth = append(rec.auth, r.Header.Get("Authorization"))
	rec.mu.Unlock()
	if rec.answer != nil && rec.answer(w, r) {
		return
	}
	w.Header().Set("x-ms-error-code", "BlobNotFound")
	w.WriteHeader(http.StatusNotFound)
}

func clearAzureEnv(t *testing.T) {
	for _, n := range []string{
		"ARM_ACCESS_KEY", "AZURE_STORAGE_KEY", "ARM_SAS_TOKEN", "AZURE_STORAGE_SAS_TOKEN",
		"ARM_TENANT_ID", "AZURE_TENANT_ID", "ARM_CLIENT_ID", "AZURE_CLIENT_ID", "ARM_CLIENT_SECRET",
		"AZURE_CLIENT_SECRET", "AZURE_RESOURCE_GROUP", "AZURE_SUBSCRIPTION_ID", "AZURE_STORAGE_ACCOUNT",
		EnvAllowInsecure,
	} {
		t.Setenv(n, "")
	}
}

const azureWaiveAll = "blob_versioning,lifecycle_management,public_access"

// Not parallel: it sets the environment and azureStoreTransport.
func TestAzureBlobStoreWithALocalEndpointStaysLocal(t *testing.T) {
	clearAzureEnv(t)
	rec := &recorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	azureStoreTransport = loopbackClient{t: t, next: http.DefaultTransport}
	t.Cleanup(func() { azureStoreTransport = nil })
	ctx := context.Background()

	base := "plunge+azblob://records/app?storage_account=devacct&endpoint=" + url.QueryEscape(srv.URL)

	// No key, no SAS, no metadata host: refused, and nothing is sent anywhere.
	_, err := openStore(ctx, base)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never falls back to the shell's Azure login")
	assert.Empty(t, rec.seen)

	// The account key: the blobs are signed with it and go to the endpoint. The settings cannot be read without
	// ARM, so the open is refused naming them unless allow_insecure waives them.
	t.Setenv("ARM_ACCESS_KEY", "dGVzdC1rZXk=")
	_, err = openStore(ctx, base)
	var refused *ContractRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Text, "access key or a SAS")

	store, err := openStore(ctx, base+"&allow_insecure="+azureWaiveAll)
	require.NoError(t, err)
	_, _, exists, err := store.Get(ctx, "stacks/dev/x")
	require.NoError(t, err)
	assert.False(t, exists)
	rec.mu.Lock()
	require.NotEmpty(t, rec.seen)
	assert.True(t, strings.HasPrefix(rec.seen[len(rec.seen)-1], "GET /records/app/.pulumi/records/stacks/dev/x"),
		rec.seen)
	assert.True(t, strings.HasPrefix(rec.auth[len(rec.auth)-1], "SharedKey devacct:"))
	rec.mu.Unlock()

	// A SAS in place of the key.
	t.Setenv("ARM_ACCESS_KEY", "")
	t.Setenv("ARM_SAS_TOKEN", "sv=2024-01-01&sig=fake")
	store, err = openStore(ctx, base+"&allow_insecure="+azureWaiveAll)
	require.NoError(t, err)
	_, _, _, err = store.Get(ctx, "stacks/dev/x")
	require.NoError(t, err)
}

// With a metadata host, ARM and the login endpoint come from it: the settings check and the token request go to the
// emulator, and instance discovery is not asked of login.microsoftonline.com.
func TestAzureBlobStoreMetadataHostKeepsTheSettingsCheckLocal(t *testing.T) {
	clearAzureEnv(t)
	rec := &recorder{}
	srv := httptest.NewTLSServer(rec)
	defer srv.Close()
	rec.answer = func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/metadata/endpoints":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resourceManager": srv.URL + "/",
				"authentication":  map[string]any{"loginEndpoint": srv.URL + "/", "audiences": []string{srv.URL + "/"}},
				"suffixes":        map[string]string{"storage": "localhost"},
			})
		case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token_endpoint":         srv.URL + "/tenant/oauth2/v2.0/token",
				"authorization_endpoint": srv.URL + "/tenant/oauth2/v2.0/authorize",
				"issuer":                 srv.URL + "/tenant/v2.0",
			})
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "fake", "token_type": "Bearer", "expires_in": 3600,
			})
		default:
			return false
		}
		return true
	}
	azureStoreTransport = loopbackClient{t: t, next: srv.Client().Transport}
	t.Cleanup(func() { azureStoreTransport = nil })
	t.Setenv("ARM_TENANT_ID", "tenant")
	t.Setenv("ARM_CLIENT_ID", "client")
	t.Setenv("ARM_CLIENT_SECRET", "secret")

	host := strings.TrimPrefix(srv.URL, "https://")
	// The fake answers the account's settings with 404, so the open fails; what matters is where it asked.
	_, _ = openStore(context.Background(), "plunge+azblob://records?storage_account=devacct&resource_group=rg"+
		"&subscription_id=sub&metadata_host="+host+"&endpoint="+url.QueryEscape(srv.URL))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	joined := strings.Join(rec.seen, "\n")
	assert.Contains(t, joined, "GET /metadata/endpoints")
	assert.Contains(t, joined, "/oauth2/v2.0/token")
	assert.Contains(t, joined, "/providers/Microsoft.Storage/storageAccounts/devacct")
}
