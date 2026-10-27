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

// The stack metadata half of plunge+azblob:// against an emulator (the follow-up to INTENTIUS/plunge#50): diy.New
// opens it through go-cloud's azureblob driver, so the URL it gets must name the record store's local endpoint and a
// shared key or SAS, or the open is refused before diy.New. go-cloud's own client does not go through any transport
// plunge sets, so the test points Azure's default credential sources at loopback tripwires that must get no
// request, swaps http.DefaultTransport and azureStoreTransport for the loopback guard, and checks that the requests
// diy.New makes all reach the fake on 127.0.0.1, signed with the shared key.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gocloud.dev/blob/azureblob"

	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"
)

// azureCloudHosts are hosts Azure's default credential chain or a real-cloud client would reach.
var azureCloudHosts = []string{
	"169.254.169.254",
	"login.microsoftonline.com",
	"devacct.blob.core.windows.net",
	"management.azure.com",
}

func TestLoopbackOnlyRefusesAzureHosts(t *testing.T) {
	t.Parallel()
	var denied []string
	guard := storeGuard{next: http.DefaultTransport, denied: func(h string) { denied = append(denied, h) }}
	for _, h := range azureCloudHosts {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://"+h+"/", nil)
		require.NoError(t, err)
		resp, err := guard.RoundTrip(req)
		if resp != nil {
			resp.Body.Close()
		}
		assert.Error(t, err, h)
	}
	assert.Len(t, denied, len(azureCloudHosts))
}

// isolateAzureEnv clears every Azure credential and setting go-cloud or azidentity would read, points the managed
// identity and login endpoints at tripwires, and sends azureStoreTransport and http.DefaultTransport through the
// loopback guard. It returns the two tripwires, which the caller checks are untouched.
func isolateAzureEnv(t *testing.T) (identity, login *tripwire) {
	clearAzureEnv(t)
	for _, n := range []string{
		"AZURE_STORAGE_CONNECTION_STRING", "AZURE_STORAGEBLOB_CONNECTIONSTRING", "AZURE_STORAGE_DOMAIN",
		"AZURE_STORAGE_PROTOCOL", "AZURE_STORAGE_IS_CDN", "AZURE_STORAGE_IS_LOCAL_EMULATOR",
		"AZURE_CLIENT_CERTIFICATE_PATH", "AZURE_FEDERATED_TOKEN_FILE", "AZURE_USERNAME", "AZURE_PASSWORD",
		"AZURE_TOKEN_CREDENTIALS",
	} {
		t.Setenv(n, "")
	}
	dir := t.TempDir()
	t.Setenv("AZURE_CONFIG_DIR", filepath.Join(dir, "az"))
	t.Setenv("AZD_CONFIG_DIR", filepath.Join(dir, "azd"))

	identity, login = newTripwire(t), newTripwire(t)
	// Managed identity: App Service's variables name the endpoint in place of 169.254.169.254.
	t.Setenv("IDENTITY_ENDPOINT", identity.srv.URL)
	t.Setenv("IDENTITY_HEADER", "tripwire")
	t.Setenv("MSI_ENDPOINT", identity.srv.URL)
	// The environment and workload identity credentials sign in at AZURE_AUTHORITY_HOST.
	t.Setenv("AZURE_AUTHORITY_HOST", login.srv.URL+"/")

	deny := func(h string) { t.Errorf("a request went to the non-local host %s", h) }
	prevDefault := http.DefaultTransport
	azureStoreTransport = loopbackClient{t: t, next: prevDefault}
	http.DefaultTransport = storeGuard{next: prevDefault, denied: deny}
	t.Cleanup(func() {
		azureStoreTransport = nil
		http.DefaultTransport = prevDefault
	})
	return identity, login
}

func assertAzureUntouched(t *testing.T, identity, login *tripwire) {
	assert.Zero(t, identity.hits.Load(), "the managed identity endpoint was asked for a token")
	assert.Zero(t, login.hits.Load(), "the login endpoint was asked for a token")
}

// goCloudServiceURL is the service URL go-cloud's opener builds from an azblob:// URL's query, by the same rules.
func goCloudServiceURL(t *testing.T, diyURL string) string {
	u, err := url.Parse(diyURL)
	require.NoError(t, err)
	q := u.Query()
	for k := range q {
		assert.Contains(t, []string{"storage_account", "domain", "protocol", "cdn", "localemu"}, k,
			"go-cloud refuses the query key %s", k)
	}
	svc, err := azureblob.NewServiceURL(&azureblob.ServiceURLOptions{
		AccountName: q.Get("storage_account"), StorageDomain: q.Get("domain"), Protocol: q.Get("protocol"),
		IsCDN: q.Get("cdn") == "true", IsLocalEmulator: q.Get("localemu") == "true",
	})
	require.NoError(t, err)
	return string(svc)
}

// Not parallel: it sets the environment, azureStoreTransport and http.DefaultTransport.
func TestAzblobMetadataURLSpellsTheEndpointForGoCloud(t *testing.T) {
	identity, login := isolateAzureEnv(t)
	ctx := context.Background()
	t.Setenv("AZURE_STORAGE_ACCOUNT", "devacct")
	t.Setenv("AZURE_STORAGE_KEY", "dGVzdC1rZXk=")

	for _, endpoint := range []string{
		"https://devacct.blob.localhost:4577", // floci-az
		"http://127.0.0.1:10000/devacct",      // Azurite's path style
		"https://localhost:4577/devacct",
	} {
		pu := "plunge+azblob://records/app?storage_account=devacct&resource_group=rg&subscription_id=sub" +
			"&allow_insecure=public_access&endpoint=" + url.QueryEscape(endpoint)
		meta, err := azblobMetadataURL(ctx, pu, innerURL(pu))
		require.NoError(t, err, endpoint)
		assert.True(t, strings.HasPrefix(meta, "azblob://records/app?"), meta)
		assert.Equal(t, endpoint, goCloudServiceURL(t, meta), meta)
	}

	// The variables go-cloud reads for the service URL are overridden by the keys written.
	t.Setenv("AZURE_STORAGE_IS_LOCAL_EMULATOR", "true")
	t.Setenv("AZURE_STORAGE_DOMAIN", "core.windows.net")
	pu := "plunge+azblob://records?storage_account=devacct&endpoint=https://devacct.blob.localhost:4577"
	meta, err := azblobMetadataURL(ctx, pu, innerURL(pu))
	require.NoError(t, err)
	assert.Contains(t, meta, "localemu=false")
	assert.Contains(t, meta, "cdn=false")

	assertAzureUntouched(t, identity, login)
}

// Not parallel: it sets the environment, azureStoreTransport and http.DefaultTransport.
func TestAzblobMetadataURLRefusals(t *testing.T) {
	identity, login := isolateAzureEnv(t)
	ctx := context.Background()
	const local = "plunge+azblob://records?storage_account=devacct&endpoint=http://127.0.0.1:10000/devacct"
	refused := func(pu, want string) {
		t.Helper()
		_, err := azblobMetadataURL(ctx, pu, innerURL(pu))
		require.Error(t, err, pu)
		assert.Contains(t, err.Error(), want, pu)
	}

	// No AZURE_STORAGE_ names: go-cloud would build DefaultAzureCredential. ARM_ACCESS_KEY reaches the record store
	// only.
	t.Setenv("ARM_ACCESS_KEY", "dGVzdC1rZXk=")
	refused(local, "AZURE_STORAGE_KEY")
	refused(local, "default credential chain")
	t.Setenv("ARM_ACCESS_KEY", "")

	// A key with no AZURE_STORAGE_ACCOUNT is not go-cloud's shared key either.
	t.Setenv("AZURE_STORAGE_KEY", "dGVzdC1rZXk=")
	refused(local, "AZURE_STORAGE_ACCOUNT and AZURE_STORAGE_KEY")

	// A connection string carries its own endpoint.
	t.Setenv("AZURE_STORAGE_CONNECTION_STRING", "DefaultEndpointsProtocol=https;AccountName=devacct;AccountKey=x")
	refused(local, "AZURE_STORAGE_CONNECTION_STRING")
	t.Setenv("AZURE_STORAGE_CONNECTION_STRING", "")

	// Another account's key.
	t.Setenv("AZURE_STORAGE_ACCOUNT", "otheracct")
	refused(local, "must match")
	t.Setenv("AZURE_STORAGE_ACCOUNT", "devacct")

	// An endpoint that is not local, and one go-cloud cannot address.
	refused("plunge+azblob://records?storage_account=devacct&endpoint=https://devacct.blob.core.windows.net",
		"not local")
	refused("plunge+azblob://records?storage_account=devacct&endpoint=http://10.0.0.5:10000/devacct", "not local")
	refused("plunge+azblob://records?storage_account=devacct&endpoint=http://127.0.0.1:10000", "cannot address")
	refused("plunge+azblob://records?storage_account=devacct&metadata_host=management.azure.com", "not local")

	// go-cloud's own emulator keys and variables would move the metadata alone.
	refused("plunge+azblob://records?storage_account=devacct&domain=localhost:4577&localemu=true", "endpoint key")
	t.Setenv("AZURE_STORAGE_DOMAIN", "blob.localhost:4577")
	refused("plunge+azblob://records?storage_account=devacct", "AZURE_STORAGE_DOMAIN")
	t.Setenv("AZURE_STORAGE_DOMAIN", "")

	// The same for a plunge+k8s:// URL's azblob:// metadata.
	t.Setenv("AZURE_STORAGE_KEY", "")
	k8s := "plunge+k8s://records?metadata=" + url.QueryEscape("azblob://meta?storage_account=devacct&"+
		"endpoint=http://127.0.0.1:10000/devacct")
	m, err := metadataURL(k8s)
	require.NoError(t, err)
	_, err = azblobMetadataURL(ctx, k8s, m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AZURE_STORAGE_KEY")

	assertAzureUntouched(t, identity, login)
}

// A metadata_host with no endpoint: the blob URL comes from the host's storage suffix, read over the guard.
func TestAzblobMetadataURLFromAMetadataHost(t *testing.T) {
	identity, login := isolateAzureEnv(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"suffixes": map[string]string{"storage": "localhost:4577"}})
	}))
	defer srv.Close()
	azureStoreTransport = loopbackClient{t: t, next: srv.Client().Transport}
	t.Setenv("AZURE_STORAGE_SAS_TOKEN", "sv=2024-01-01&sig=fake")

	pu := "plunge+azblob://records?storage_account=devacct&metadata_host=" + strings.TrimPrefix(srv.URL, "https://")
	meta, err := azblobMetadataURL(context.Background(), pu, innerURL(pu))
	require.NoError(t, err)
	assert.NotContains(t, meta, "metadata_host")
	assert.Equal(t, "https://devacct.blob.localhost:4577", goCloudServiceURL(t, meta))

	assertAzureUntouched(t, identity, login)
}

// Real-Azure URLs: plunge's keys are dropped, which go-cloud would refuse, and nothing else changes or is read.
func TestAzblobMetadataURLRealAzure(t *testing.T) {
	isolateAzureEnv(t)
	pu := "plunge+azblob://records/app?storage_account=prodacct&resource_group=rg&subscription_id=sub" +
		"&allow_insecure=public_access"
	meta, err := azblobMetadataURL(context.Background(), pu, innerURL(pu))
	require.NoError(t, err)
	assert.Equal(t, "azblob://records/app?storage_account=prodacct", meta)

	for _, u := range []string{"s3://b?endpoint=http://localhost:4566", "gs://b", "file:///tmp/x"} {
		got, err := azblobMetadataURL(context.Background(), "plunge+"+u, u)
		require.NoError(t, err)
		assert.Equal(t, u, got)
	}
}

// Not parallel: it sets the environment, azureStoreTransport and http.DefaultTransport.
func TestAzblobNewKeepsTheMetadataLocal(t *testing.T) {
	identity, login := isolateAzureEnv(t)
	rec := &recorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	sink := diag.DefaultSink(io.Discard, io.Discard, diag.FormatOptions{Color: colors.Never})
	ctx := context.Background()

	pu := "plunge+azblob://records/app?storage_account=devacct&allow_insecure=" + azureWaiveAll +
		"&endpoint=" + url.QueryEscape(srv.URL+"/devacct")

	// No credential go-cloud reads: refused before diy.New, and nothing is sent anywhere.
	t.Setenv("ARM_ACCESS_KEY", "dGVzdC1rZXk=")
	_, err := New(ctx, sink, pu, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AZURE_STORAGE_KEY")
	assert.Empty(t, rec.seen)
	t.Setenv("ARM_ACCESS_KEY", "")

	// The shared key: diy.New's requests go to the fake under the account's path, signed with it. The fake answers
	// 404, so the open may fail; what matters is where it asked and with what.
	t.Setenv("AZURE_STORAGE_ACCOUNT", "devacct")
	t.Setenv("AZURE_STORAGE_KEY", "dGVzdC1rZXk=")
	_, _ = New(ctx, sink, pu, nil)
	rec.mu.Lock()
	require.NotEmpty(t, rec.seen)
	assert.Contains(t, strings.Join(rec.seen, "\n"), "/devacct/records", rec.seen)
	for _, a := range rec.auth {
		assert.True(t, strings.HasPrefix(a, "SharedKey devacct:"), a)
	}
	rec.mu.Unlock()

	assertAzureUntouched(t, identity, login)
}

// urlBackend is a DIY backend stand-in that reports the rewritten metadata URL.
type urlBackend struct {
	backend.Backend
	url string
}

func (b urlBackend) URL() string { return b.url }

// The plunge URL saved at login keeps its endpoint: URL is the one given, not the DIY backend's rewritten one,
// which would open the record store on real Azure.
func TestAzblobBackendURLIsTheOneGiven(t *testing.T) {
	t.Parallel()
	pu := "plunge+azblob://records?storage_account=devacct&endpoint=http://127.0.0.1:10000/devacct"
	b := newBackend(nil, pu, urlBackend{url: "azblob://records?domain=127.0.0.1%3A10000&localemu=true"}, nil)
	assert.Equal(t, pu, b.URL())
}
