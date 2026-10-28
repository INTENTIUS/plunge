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

// INTENTIUS/plunge#39: choudoufu's exported store suite (staterecordtest.RunConformance) run against each store
// plunge opens, opened the way a run opens it: through openStore and a plunge URL, so the URL parsing, the key
// prefix and the settings check on open are all in the path under test.
//
// plunge+file:// runs on a temp dir. The bucket stores need a store with real conditional-write semantics, which
// the suite exercises in full (versions, create-if-absent, delete on a version, paged lists), and choudoufu's
// in-process fakes for them are unexported test files. So those legs run against a scratch emulator, named by an
// environment variable, and skip without it:
//
//	PLUNGE_CONFORMANCE_S3_URL      plunge+s3://<bucket>?endpoint=http://localhost:4566&region=us-east-1&s3ForcePathStyle=true
//	                               (floci; AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY set to the emulator's)
//	PLUNGE_CONFORMANCE_GS_URL      plunge+gs://<bucket>, with STORAGE_EMULATOR_HOST=localhost:4588 (floci-gcp)
//	PLUNGE_CONFORMANCE_AZBLOB_URL  plunge+azblob://<container>?storage_account=<a>&metadata_host=localhost:4577&...
//	                               (floci-az; ARM_TENANT_ID, ARM_CLIENT_ID and ARM_CLIENT_SECRET set, per #35)
//
// The bucket or container must carry the settings openStore checks (versioning, a noncurrent-version lifecycle
// rule, the public access settings), as secure_bucket in scripts/plunge-races/lib.sh sets them, or name them in allow_insecure.
// Each subtest gets its own key prefix under the URL's path, so subtests never see each other's keys.
//
// No leg reaches a public endpoint: each refuses, before opening anything, a URL whose endpoint is not a loopback
// host, and the GCS and S3 legs clear the credential sources that would fetch a token from a real cloud.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/intentius/choudoufu/pkg/staterecord/staterecordtest"
	"github.com/stretchr/testify/require"
)

func TestPlungeFileStoreConformance(t *testing.T) {
	t.Parallel()
	staterecordtest.RunConformance(t, func(t *testing.T) staterecord.Store {
		store, err := openStore(context.Background(), URLPrefix+"file://"+t.TempDir())
		require.NoError(t, err)
		return store
	})
}

// TestPlungeS3StoreConformance is an emulator run (floci): epic-ready or the maintainer runs it.
func TestPlungeS3StoreConformance(t *testing.T) {
	raw := conformanceURL(t, "PLUNGE_CONFORMANCE_S3_URL", "s3")
	q := innerQuery(t, raw)
	requireLoopback(t, "endpoint", q.Get("endpoint"))
	for _, n := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		if os.Getenv(n) == "" {
			t.Fatalf("%s is unset: set the emulator's static credentials, so the AWS default chain looks no further", n)
		}
	}
	// The static keys above come first in the chain; these keep it from reading a profile or asking IMDS.
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", os.DevNull)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", os.DevNull)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	runURLConformance(t, raw)
}

// TestPlungeGSStoreConformance is an emulator run (floci-gcp): epic-ready or the maintainer runs it.
func TestPlungeGSStoreConformance(t *testing.T) {
	raw := conformanceURL(t, "PLUNGE_CONFORMANCE_GS_URL", "gs")
	// With STORAGE_EMULATOR_HOST set the storage client goes anonymous to that host and looks up no credential.
	requireLoopback(t, "STORAGE_EMULATOR_HOST", os.Getenv("STORAGE_EMULATOR_HOST"))
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("CLOUDSDK_CONFIG", t.TempDir())
	t.Setenv("GCE_METADATA_HOST", "127.0.0.1:9")
	runURLConformance(t, raw)
}

// TestPlungeAzblobStoreConformance is an emulator run (floci-az): epic-ready or the maintainer runs it.
func TestPlungeAzblobStoreConformance(t *testing.T) {
	raw := conformanceURL(t, "PLUNGE_CONFORMANCE_AZBLOB_URL", "azblob")
	q := innerQuery(t, raw)
	if q.Get("endpoint") == "" && q.Get("metadata_host") == "" {
		t.Fatalf("%s names neither endpoint nor metadata_host; without one the store would open against real Azure", raw)
	}
	requireLoopback(t, "endpoint", q.Get("endpoint"))
	requireLoopback(t, "metadata_host", q.Get("metadata_host"))
	runURLConformance(t, raw)
}

// conformanceURL is the plunge URL env names for scheme, or a skip when it is unset.
func conformanceURL(t *testing.T, env, scheme string) string {
	raw := os.Getenv(env)
	if raw == "" {
		t.Skipf("%s is unset: this leg runs against a scratch emulator (see conformance_test.go)", env)
	}
	if !strings.HasPrefix(raw, URLPrefix+scheme+"://") {
		t.Fatalf("%s=%q is not a %s%s:// URL", env, raw, URLPrefix, scheme)
	}
	return raw
}

func innerQuery(t *testing.T, raw string) url.Values {
	u, err := url.Parse(innerURL(raw))
	require.NoError(t, err)
	return u.Query()
}

// requireLoopback fails the test when value names a host that is not loopback. An empty value passes; each leg
// checks separately that some endpoint is named.
func requireLoopback(t *testing.T, name, value string) {
	if value != "" && !isLoopbackHost(value) {
		t.Fatalf("%s %q is not a loopback host: the conformance legs run against a local emulator only", name, value)
	}
}

var conformanceSeq atomic.Int64

// runURLConformance runs the suite on raw, each subtest under its own key prefix below raw's path.
func runURLConformance(t *testing.T, raw string) {
	run := fmt.Sprintf("conformance-%d", os.Getpid())
	staterecordtest.RunConformance(t, func(t *testing.T) staterecord.Store {
		u, err := url.Parse(innerURL(raw))
		require.NoError(t, err)
		u.Path = strings.TrimSuffix(u.Path, "/") + fmt.Sprintf("/%s/%d", run, conformanceSeq.Add(1))
		store, err := openStore(context.Background(), URLPrefix+u.String())
		require.NoError(t, err)
		return store
	})
}
