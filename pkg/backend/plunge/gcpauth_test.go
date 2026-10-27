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

// The acceptance test of INTENTIUS/plunge#42: when the stack's google provider names custom endpoints, no GCP token
// source plunge builds reaches a non-local host. Everything here is loopback: the "emulator" is an httptest server,
// and the credentials JSON's token_uri and the GCE metadata host point at a loopback trap that fails the test if it
// is ever asked. Every request goes through a transport that refuses any non-loopback host, oauth2.googleapis.com,
// metadata.google.internal and 169.254.169.254 among them.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/intentius/choudoufu/pkg/gcpsweep"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// gcpLoopbackGuard sends loopback requests over its own transport and refuses every other host, recording it.
type gcpLoopbackGuard struct {
	inner    http.RoundTripper
	mu       sync.Mutex
	nonLocal []string
}

func newGCPLoopbackGuard() *gcpLoopbackGuard {
	return &gcpLoopbackGuard{inner: &http.Transport{Proxy: nil}}
}

func (g *gcpLoopbackGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Hostname()
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		g.mu.Lock()
		g.nonLocal = append(g.nonLocal, r.URL.Host)
		g.mu.Unlock()
		return nil, fmt.Errorf("test: %s is not a loopback host", r.URL.Host)
	}
	return g.inner.RoundTrip(r)
}

func (g *gcpLoopbackGuard) refused() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.nonLocal...)
}

// The guard itself: Google's auth hosts and the metadata server are refused, a loopback server is reached.
func TestGCPLoopbackGuardRefusesGooglesAuthHosts(t *testing.T) {
	t.Parallel()
	g := newGCPLoopbackGuard()
	c := &http.Client{Transport: g}
	for _, u := range []string{
		"https://oauth2.googleapis.com/token",
		"https://www.googleapis.com/oauth2/v4/token",
		"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/sa:generateAccessToken",
		"http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token",
		"http://169.254.169.254/computeMetadata/v1/instance/service-accounts/default/token",
	} {
		resp, err := c.Get(u) //nolint:noctx // a refused request never leaves the process
		if resp != nil {
			resp.Body.Close()
		}
		require.Error(t, err, u)
		assert.False(t, isLoopbackEndpoint(u), u)
	}
	assert.Len(t, g.refused(), 5)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	resp, err := c.Get(srv.URL) //nolint:noctx
	require.NoError(t, err)
	resp.Body.Close()
	assert.True(t, isLoopbackEndpoint(srv.URL))
	assert.True(t, isLoopbackEndpoint("localhost:4588"))
	assert.Len(t, g.refused(), 5)
}

// fakeServiceAccountJSON writes a service account key whose token_uri is tokenURI, signed with a fresh key, so a
// client that wrongly used it would really ask tokenURI for a token.
func fakeServiceAccountJSON(t *testing.T, tokenURI string) string {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	b, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "plc",
		"private_key_id": "fake",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":   "lister@plc.iam.gserviceaccount.com",
		"client_id":      "1",
		"token_uri":      tokenURI,
	})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "sa.json")
	require.NoError(t, os.WriteFile(path, b, 0o600))
	return path
}

// Not parallel: it sets the environment, listerTransport and http.DefaultTransport.
func TestGCPListersFetchNoTokenFromANonLocalHost(t *testing.T) {
	for _, n := range append(gcpsweep.EnvNames(), envNamesWith("GOOGLE_", "_CUSTOM_ENDPOINT")...) {
		t.Setenv(n, "")
	}
	guard := newGCPLoopbackGuard()
	listerTransport = guard
	saved := http.DefaultTransport
	http.DefaultTransport = guard
	t.Cleanup(func() {
		listerTransport = nil
		http.DefaultTransport = saved
	})

	// The trap stands in for oauth2.googleapis.com and the metadata server: any request to it is a token fetch.
	var trapMu sync.Mutex
	var trapped []string
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trapMu.Lock()
		trapped = append(trapped, r.Method+" "+r.URL.Path)
		trapMu.Unlock()
		http.Error(w, "a token was fetched", http.StatusTeapot)
	}))
	defer trap.Close()
	saPath := fakeServiceAccountJSON(t, trap.URL+"/token")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", saPath)
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(trap.URL, "http://"))
	t.Setenv("GCE_METADATA_IP", strings.TrimPrefix(trap.URL, "http://"))
	t.Setenv("CLOUDSDK_CONFIG", t.TempDir())

	urnTag := "urn-digest"
	want := map[string]string{markers.TagURN: urnTag}
	var mu sync.Mutex
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, ":generateAccessToken"):
			_ = json.NewEncoder(w).Encode(map[string]string{
				"accessToken": "minted", "expireTime": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			})
		case strings.HasSuffix(r.URL.Path, ":searchAllResources"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
				"name": "//pubsub.googleapis.com/projects/plc/topics/events", "assetType": "pubsub.googleapis.com/Topic",
				"labels": map[string]string{markers.TagURN: urnTag},
			}}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"topics": []map[string]any{{
				"name": "projects/plc/topics/events", "labels": map[string]string{markers.TagURN: urnTag},
			}}})
		}
	}))
	defer srv.Close()

	urn := resource.NewURN("floci", "test", "", "gcp:pubsub/topic:Topic", "events")
	info := TypeInfo{Type: "gcp:pubsub/topic:Topic", Family: FamilyGoogle, TFType: "google_pubsub_topic"}
	inputs := resource.NewPropertyMapFromMap(map[string]any{"name": "events"})
	local := map[string]any{"pubsubCustomEndpoint": srv.URL + "/v1/", "cloudAssetCustomEndpoint": srv.URL + "/v1/"}
	with := func(extra map[string]any) deploy.BindRequest {
		m := map[string]any{"project": "plc"}
		for k, v := range local {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return deploy.BindRequest{URN: urn, Inputs: inputs, ProviderInputs: resource.NewPropertyMapFromMap(m)}
	}

	cases := []struct {
		name  string
		extra map[string]any
		// auth is the Authorization header the listing call carries; skip is the skipped service, when skipped.
		auth string
		skip string
	}{
		{"application default credentials in reach", nil, "", ""},
		{"a credentials JSON", map[string]any{"credentials": saPath}, "", ""},
		{"a static access token", map[string]any{"accessToken": "floci-token", "credentials": saPath},
			"Bearer floci-token", ""},
		{"impersonation through the stack's IAM endpoint", map[string]any{
			"credentials": saPath, "impersonateServiceAccount": "sa@plc.iam.gserviceaccount.com",
			"iamCredentialsCustomEndpoint": srv.URL + "/iam/v1/",
		}, "Bearer minted", ""},
		{"a non-local custom endpoint with a credentials JSON", map[string]any{
			"credentials": saPath, "storageCustomEndpoint": "https://storage.example.com/storage/v1/",
		}, "", "pubsub.googleapis.com"},
		{"a non-local custom endpoint with ADC", map[string]any{
			"storageCustomEndpoint": "https://storage.example.com/storage/v1/",
		}, "", "pubsub.googleapis.com"},
		{"impersonation minted off a non-local host", map[string]any{
			"accessToken": "floci-token", "impersonateServiceAccount": "sa@plc.iam.gserviceaccount.com",
			"iamCredentialsCustomEndpoint": "https://iam.example.com/v1/",
		}, "", "iamcredentials.googleapis.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mu.Lock()
			auths = nil
			mu.Unlock()
			req := with(c.extra)
			objs, err := newGCPLister().ListMarked(context.Background(), info, req, want)
			if c.skip != "" {
				var skip *SkippedListing
				require.ErrorAs(t, err, &skip)
				assert.Equal(t, c.skip, skip.Service)
				assert.Equal(t, "floci", skip.Stack)
				mu.Lock()
				assert.Empty(t, auths, "nothing was listed")
				mu.Unlock()
				return
			}
			require.NoError(t, err)
			require.Len(t, objs, 1)
			mu.Lock()
			require.NotEmpty(t, auths)
			assert.Equal(t, c.auth, auths[len(auths)-1])
			mu.Unlock()

			// Asset Inventory, the fallback lister, goes over the same client.
			objs, err = (&assetLister{}).ListMarked(context.Background(), info, req, want)
			require.NoError(t, err)
			require.Len(t, objs, 1)
			mu.Lock()
			assert.Equal(t, c.auth, auths[len(auths)-1])
			mu.Unlock()
		})
	}

	trapMu.Lock()
	assert.Empty(t, trapped, "no token was fetched from the credentials JSON's token_uri or the metadata server")
	trapMu.Unlock()
	assert.Empty(t, guard.refused(), "no request went to a non-loopback host")
}
