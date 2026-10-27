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

// The acceptance test of INTENTIUS/plunge#41: with the azure provider's metadataHost on localhost, the ARM listing
// goes to the ARM endpoint that host names, the token comes from its login endpoint with a client secret from the
// environment, and no request reaches a host that is not local.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// loopbackTLS sends loopback requests through next (the fake's TLS client transport) and fails the test on any
// other host.
type loopbackTLS struct {
	t    *testing.T
	next http.RoundTripper
}

func (l loopbackTLS) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Hostname()
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		l.t.Errorf("a request went to the non-local host %s", r.URL.Host)
		return nil, fmt.Errorf("test: %s is not a loopback host", r.URL.Host)
	}
	return l.next.RoundTrip(r)
}

// fakeAzure is a loopback floci-az: /metadata/endpoints, the login endpoint's discovery and token, and ARM's
// resource and resource group lists.
type fakeAzure struct {
	mu   sync.Mutex
	seen []string
	auth map[string]string // path -> Authorization header
	// login overrides the login endpoint /metadata/endpoints names.
	login string
	tags  map[string]string
}

func (f *fakeAzure) handler(base func() string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.Method+" "+r.URL.Path)
		f.auth[r.URL.Path] = r.Header.Get("Authorization")
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		srv := base()
		switch {
		case r.URL.Path == "/metadata/endpoints":
			login := srv + "/"
			if f.login != "" {
				login = f.login
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resourceManager": srv + "/",
				"authentication":  map[string]any{"loginEndpoint": login, "audiences": []string{srv + "/"}},
				"suffixes":        map[string]string{"storage": "localhost"},
			})
		case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token_endpoint":         srv + "/tenant/oauth2/v2.0/token",
				"authorization_endpoint": srv + "/tenant/oauth2/v2.0/authorize",
				"issuer":                 srv + "/tenant/v2.0",
			})
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "fake-arm-token", "token_type": "Bearer", "expires_in": 3600,
			})
		case r.URL.Path == "/subscriptions/sub/resources":
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]any{
				{"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct",
					"tags": f.tags},
				{"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/other",
					"tags": map[string]string{markers.TagURN: "someone-else"}},
			}})
		case r.URL.Path == "/subscriptions/sub/resourcegroups":
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []any{}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (f *fakeAzure) requests() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.seen, "\n")
}

func setAzureClientSecretEnv(t *testing.T) {
	t.Setenv("ARM_TENANT_ID", "tenant")
	t.Setenv("ARM_CLIENT_ID", "client")
	t.Setenv("ARM_CLIENT_SECRET", "secret")
}

// Not parallel: it sets the environment and listerTransport.
func TestARMListingGoesThroughTheMetadataHost(t *testing.T) {
	clearAzureEnv(t)
	for _, n := range []string{"ARM_METADATA_HOSTNAME", "ARM_SUBSCRIPTION_ID", "ARM_ENVIRONMENT", "AZURE_ENVIRONMENT",
		"ARM_RESOURCE_MANAGER_ENDPOINT", "ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST"} {
		t.Setenv(n, "")
	}
	urn := resource.NewURN("flociaz", "test", "", "azure-native:storage:StorageAccount", "acct")
	want := markedWith(t, urn, markers.EncodingTags, nil)

	newFake := func(t *testing.T) (*fakeAzure, string) {
		f := &fakeAzure{auth: map[string]string{}, tags: want}
		var srv *httptest.Server
		srv = httptest.NewTLSServer(f.handler(func() string { return srv.URL }))
		t.Cleanup(srv.Close)
		listerTransport = loopbackTLS{t: t, next: srv.Client().Transport}
		t.Cleanup(func() { listerTransport = nil })
		return f, strings.TrimPrefix(srv.URL, "https://")
	}
	request := func(host string) deploy.BindRequest {
		return deploy.BindRequest{URN: urn, ProviderInputs: resource.NewPropertyMapFromMap(map[string]any{
			"metadataHost": host, "subscriptionId": "sub",
		})}
	}

	t.Run("lists through the metadata host's ARM with its login's token", func(t *testing.T) {
		setAzureClientSecretEnv(t)
		f, host := newFake(t)
		objs, err := (&armLister{}).ListMarked(context.Background(), TypeInfo{}, request(host), want)
		require.NoError(t, err)
		require.Len(t, objs, 1)
		assert.Equal(t,
			resource.ID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct"),
			objs[0].ID)
		seen := f.requests()
		assert.Contains(t, seen, "GET /metadata/endpoints")
		assert.Contains(t, seen, "/oauth2/v2.0/token")
		assert.Contains(t, seen, "GET /subscriptions/sub/resources")
		assert.Contains(t, seen, "GET /subscriptions/sub/resourcegroups")
		f.mu.Lock()
		assert.Equal(t, "Bearer fake-arm-token", f.auth["/subscriptions/sub/resources"])
		f.mu.Unlock()
	})

	t.Run("the environment variable names the metadata host", func(t *testing.T) {
		setAzureClientSecretEnv(t)
		f, host := newFake(t)
		t.Setenv("ARM_METADATA_HOSTNAME", host)
		req := deploy.BindRequest{URN: urn, ProviderInputs: resource.NewPropertyMapFromMap(map[string]any{
			"subscriptionId": "sub",
		})}
		objs, err := (&armLister{}).ListMarked(context.Background(), TypeInfo{}, req, want)
		require.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Contains(t, f.requests(), "GET /subscriptions/sub/resources")
	})

	t.Run("no client secret in the environment skips before any request", func(t *testing.T) {
		f, host := newFake(t)
		// The provider's own clientSecret is not read on this path, and the default chain is never tried.
		req := request(host)
		req.ProviderInputs["clientSecret"] = resource.NewProperty("from-config")
		req.ProviderInputs["clientId"] = resource.NewProperty("client")
		req.ProviderInputs["tenantId"] = resource.NewProperty("tenant")
		_, err := (&armLister{}).ListMarked(context.Background(), TypeInfo{}, req, want)
		var skip *SkippedListing
		require.ErrorAs(t, err, &skip)
		assert.Equal(t, "ARM", skip.Service)
		assert.Equal(t, "flociaz", skip.Stack)
		assert.Contains(t, skip.Error(), "ARM_CLIENT_SECRET")
		assert.Contains(t, skip.Error(), host)
		assert.Empty(t, f.requests())
	})

	t.Run("a local metadata host naming a non-local login is skipped", func(t *testing.T) {
		setAzureClientSecretEnv(t)
		f, host := newFake(t)
		f.login = "https://login.microsoftonline.com/"
		_, err := (&armLister{}).ListMarked(context.Background(), TypeInfo{}, request(host), want)
		var skip *SkippedListing
		require.ErrorAs(t, err, &skip)
		assert.Contains(t, skip.Error(), "login.microsoftonline.com")
		seen := f.requests()
		assert.Equal(t, "GET /metadata/endpoints", seen, "only the metadata was read")
	})
}

// An azure provider naming custom endpoints other than a metadata host is skipped, never listed through the public
// cloud or the default credential chain.
func TestARMListingSkipsOtherCustomEndpoints(t *testing.T) {
	clearAzureEnv(t)
	for _, n := range []string{"ARM_METADATA_HOSTNAME", "ARM_ENVIRONMENT", "AZURE_ENVIRONMENT",
		"ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST"} {
		t.Setenv(n, "")
	}
	t.Setenv("ARM_RESOURCE_MANAGER_ENDPOINT", "https://localhost:4577/")
	guard := &loopbackOnly{t: t}
	listerTransport = guard
	t.Cleanup(func() { listerTransport = nil })

	urn := resource.NewURN("flociaz", "test", "", "azure-native:storage:StorageAccount", "acct")
	req := deploy.BindRequest{URN: urn, ProviderInputs: resource.NewPropertyMapFromMap(map[string]any{
		"subscriptionId": "sub",
	})}
	_, err := (&armLister{}).ListMarked(context.Background(), TypeInfo{}, req, map[string]string{})
	var skip *SkippedListing
	require.ErrorAs(t, err, &skip)
	assert.Contains(t, skip.Error(), "ARM_RESOURCE_MANAGER_ENDPOINT")
	assert.Empty(t, guard.nonLocal)
}

func TestIsLoopbackHost(t *testing.T) {
	t.Parallel()
	for host, want := range map[string]bool{
		"localhost:4577":                     true,
		"127.0.0.1:4577":                     true,
		"[::1]:4577":                         true,
		"https://localhost:4577/":            true,
		"https://acct.blob.localhost:4577/":  true,
		"https://management.azure.com/":      false,
		"https://login.microsoftonline.com/": false,
		"10.0.0.1":                           false,
	} {
		assert.Equal(t, want, isLoopbackHost(host), host)
	}
}
