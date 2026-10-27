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

// The acceptance test of INTENTIUS/plunge#50: a plunge+s3:// URL with an endpoint and a plunge+gs:// URL under
// STORAGE_EMULATOR_HOST send nothing to a host that is not the loopback address, and never build a default
// credential chain. Every request the stores make goes through storeGuard, which fails the test on any other
// host. The default chains' own clients do not go through that transport, so the test also points the EC2 instance
// metadata service and the GCE metadata server at loopback tripwires and requires that neither is asked anything,
// and swaps http.DefaultTransport for the guard.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeGuard is a transport that refuses any request whose host is not localhost or a loopback address, and
// reports it through denied.
type storeGuard struct {
	next   http.RoundTripper
	denied func(host string)
}

func (l storeGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Hostname()
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		l.denied(r.URL.Host)
		return nil, fmt.Errorf("test: %s is not a loopback host", r.URL.Host)
	}
	return l.next.RoundTrip(r)
}

// cloudHosts are hosts a default credential chain or a real-cloud client would reach.
var cloudHosts = []string{
	"169.254.169.254",
	"[fd00:ec2::254]",
	"metadata.google.internal",
	"oauth2.googleapis.com",
	"sts.amazonaws.com",
	"sts.us-east-1.amazonaws.com",
	"portal.sso.us-east-1.amazonaws.com",
	"oidc.us-east-1.amazonaws.com",
	"s3.amazonaws.com",
	"storage.googleapis.com",
}

func TestLoopbackOnlyRefusesCloudHosts(t *testing.T) {
	t.Parallel()
	var denied []string
	guard := storeGuard{next: http.DefaultTransport, denied: func(h string) { denied = append(denied, h) }}
	for _, h := range cloudHosts {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+h+"/", nil)
		require.NoError(t, err)
		resp, err := guard.RoundTrip(req)
		if resp != nil {
			resp.Body.Close()
		}
		assert.Error(t, err, h)
	}
	assert.Len(t, denied, len(cloudHosts))
}

// tripwire is a loopback server standing in for a metadata service; any request to it is a default chain at work.
type tripwire struct {
	srv  *httptest.Server
	hits atomic.Int32
}

func newTripwire(t *testing.T) *tripwire {
	tw := &tripwire{}
	tw.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw.hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(tw.srv.Close)
	return tw
}

// isolateCloudEnv clears every credential the SDKs would find, points the metadata services at tripwires, and sends
// the stores and http.DefaultTransport through the loopback guard. It returns the two tripwires, which the caller
// checks are untouched.
func isolateCloudEnv(t *testing.T) (imds, gce *tripwire) {
	dir := t.TempDir()
	for _, n := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE",
		"AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_S3", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN", "AWS_REGION",
		"AWS_DEFAULT_REGION", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CREDENTIALS", "GOOGLE_OAUTH_ACCESS_TOKEN", "STORAGE_EMULATOR_HOST",
		EnvAllowInsecure,
	} {
		t.Setenv(n, "")
	}
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "aws-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "aws-credentials"))
	t.Setenv("CLOUDSDK_CONFIG", filepath.Join(dir, "gcloud"))

	imds, gce = newTripwire(t), newTripwire(t)
	t.Setenv("AWS_EC2_METADATA_SERVICE_ENDPOINT", imds.srv.URL)
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(gce.srv.URL, "http://"))

	deny := func(h string) { t.Errorf("a request went to the non-local host %s", h) }
	prevDefault := http.DefaultTransport
	localStoreTransport = storeGuard{next: prevDefault, denied: deny}
	http.DefaultTransport = storeGuard{next: prevDefault, denied: deny}
	t.Cleanup(func() {
		localStoreTransport = nil
		http.DefaultTransport = prevDefault
	})
	return imds, gce
}

func assertUntouched(t *testing.T, imds, gce *tripwire) {
	assert.Zero(t, imds.hits.Load(), "the EC2 instance metadata service was asked for a credential")
	assert.Zero(t, gce.hits.Load(), "the GCE metadata server was asked for a credential")
}

// Not parallel: it sets the environment, localStoreTransport and http.DefaultTransport.
func TestS3StoreWithAnEndpointStaysLocal(t *testing.T) {
	imds, gce := isolateCloudEnv(t)
	rec := &recorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	ctx := context.Background()

	base := "plunge+s3://records/app?s3ForcePathStyle=true&endpoint=" + url.QueryEscape(srv.URL)

	// No keys: refused before the DIY backend and the store are opened, and nothing is sent anywhere.
	err := refuseDefaultChains(base, innerURL(base))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never falls back to the AWS default credential chain")
	_, err = openStore(ctx, base)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AWS_ACCESS_KEY_ID")
	assert.Empty(t, rec.seen)

	// The endpoint from AWS_ENDPOINT_URL_S3 counts as custom too.
	t.Setenv("AWS_ENDPOINT_URL_S3", srv.URL)
	_, err = openStore(ctx, "plunge+s3://records/app?s3ForcePathStyle=true")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never falls back")
	t.Setenv("AWS_ENDPOINT_URL_S3", "")

	// A profile is refused even with the keys set, since it outranks them in the SDK.
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDLOCAL")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "local-secret")
	_, err = openStore(ctx, base+"&profile=sso-dev")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "profile")
	require.Error(t, refuseDefaultChains(base+"&profile=sso-dev", innerURL(base+"&profile=sso-dev")))
	assert.Empty(t, rec.seen)

	// The keys: the settings check goes to the endpoint, signed with them. The fake answers 404, so the open fails;
	// what matters is where it asked and with what.
	require.NoError(t, refuseDefaultChains(base, innerURL(base)))
	_, _ = openStore(ctx, base)
	rec.mu.Lock()
	require.NotEmpty(t, rec.seen)
	assert.Contains(t, strings.Join(rec.seen, "\n"), " /records", rec.seen)
	for _, a := range rec.auth {
		assert.Contains(t, a, "Credential=AKIDLOCAL/")
	}
	rec.mu.Unlock()

	assertUntouched(t, imds, gce)
}

// Not parallel: it sets the environment, localStoreTransport and http.DefaultTransport.
func TestGCSStoreWithAnEmulatorStaysLocal(t *testing.T) {
	imds, gce := isolateCloudEnv(t)
	rec := &recorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	ctx := context.Background()
	t.Setenv("STORAGE_EMULATOR_HOST", strings.TrimPrefix(srv.URL, "http://"))

	base := "plunge+gs://records/app"

	// An endpoint key is refused, naming STORAGE_EMULATOR_HOST, before anything is sent.
	withKey := base + "?endpoint=" + url.QueryEscape(srv.URL)
	err := refuseDefaultChains(withKey, innerURL(withKey))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "STORAGE_EMULATOR_HOST")
	_, err = openStore(ctx, withKey)
	require.Error(t, err)
	assert.Empty(t, rec.seen)

	// No token: the DIY metadata store would look up Application Default Credentials, so New refuses. The record
	// store alone goes anonymous.
	err = refuseDefaultChains(base, innerURL(base))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GOOGLE_OAUTH_ACCESS_TOKEN")
	_, _ = openStore(ctx, base)
	rec.mu.Lock()
	require.NotEmpty(t, rec.seen)
	assert.Contains(t, strings.Join(rec.seen, "\n"), "/b/records")
	for _, a := range rec.auth {
		assert.Empty(t, a)
	}
	n := len(rec.seen)
	rec.mu.Unlock()

	// A static token: sent as given, and nothing fetches one.
	t.Setenv("GOOGLE_OAUTH_ACCESS_TOKEN", "local-token")
	require.NoError(t, refuseDefaultChains(base, innerURL(base)))
	_, _ = openStore(ctx, base)
	rec.mu.Lock()
	require.Greater(t, len(rec.seen), n)
	for _, a := range rec.auth[n:] {
		assert.Equal(t, "Bearer local-token", a)
	}
	rec.mu.Unlock()

	assertUntouched(t, imds, gce)
}

// A plunge+k8s:// URL whose stack metadata is an emulator's S3 bucket is refused the same way.
func TestK8sMetadataOnAnS3EndpointIsChecked(t *testing.T) {
	isolateCloudEnv(t)
	u := "plunge+k8s://records?metadata=" + url.QueryEscape("s3://meta?endpoint=http://localhost:4566")
	meta, err := metadataURL(u)
	require.NoError(t, err)
	err = refuseDefaultChains(u, meta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AWS_ACCESS_KEY_ID")
}

// Real-cloud URLs are left to the SDKs' chains, as before: the check refuses nothing and reads nothing.
func TestRealCloudStoreURLsAreNotRefused(t *testing.T) {
	isolateCloudEnv(t)
	for _, u := range []string{"plunge+s3://records/app?region=us-west-2", "plunge+gs://records/app"} {
		assert.NoError(t, refuseDefaultChains(u, innerURL(u)), u)
	}
}
