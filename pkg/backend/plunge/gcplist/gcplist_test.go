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

package gcplist

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCoveredTypesMatchTheSharedList: plunge lists exactly the types in testdata/covered-types.txt, the copy of
// choudoufu's internal/live/gcpsweep/testdata/covered-types.txt that choudoufu tests its own set against
// (INTENTIUS/plunge#15's ruling: the same per-service listers in both repos). When choudoufu's list changes, copy the
// file here.
func TestCoveredTypesMatchTheSharedList(t *testing.T) {
	t.Parallel()
	f, err := os.Open(filepath.Join("testdata", "covered-types.txt"))
	require.NoError(t, err)
	defer f.Close()
	var want []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			want = append(want, line)
		}
	}
	require.NoError(t, sc.Err())
	assert.Equal(t, want, Covered())
}

// TestServerAssignedTypesListAtTheirCollection is one list per server-assigned family: the URL is the type's
// collection under its root (a regional host filled with the location), or under a custom endpoint, and an item is
// named by its full relative name.
func TestServerAssignedTypesListAtTheirCollection(t *testing.T) {
	t.Parallel()
	vars := func(name string) (string, bool) {
		switch name {
		case "project":
			return "p1", true
		case "region", "location":
			return "us-central1", true
		}
		return "", false
	}
	for _, tc := range []struct{ tfType, url string }{
		{"google_vertex_ai_index_endpoint",
			"https://us-central1-aiplatform.googleapis.com/v1/projects/p1/locations/us-central1/indexEndpoints"},
		{"google_document_ai_schema",
			"https://us-central1-documentai.googleapis.com/v1/projects/p1/locations/us-central1/schemas"},
		{"google_transcoder_job", "https://transcoder.googleapis.com/v1/projects/p1/locations/us-central1/jobs"},
		{"google_service_directory_namespace",
			"https://servicedirectory.googleapis.com/v1/projects/p1/locations/us-central1/namespaces"},
		{"google_recaptcha_enterprise_key", "https://recaptchaenterprise.googleapis.com/v1/projects/p1/keys"},
		{"google_active_directory_domain",
			"https://managedidentities.googleapis.com/v1/projects/p1/locations/global/domains"},
	} {
		ty, reason, ok := Lookup(tc.tfType)
		require.True(t, ok, reason)
		u, _, err := ty.URL(ty.Root(""), vars)
		require.NoError(t, err)
		assert.Equal(t, tc.url, u, tc.tfType)
	}

	ty, _, _ := Lookup("google_vertex_ai_dataset")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/projects/p1/locations/us-central1/datasets", r.URL.Path)
		_, _ = w.Write([]byte(`{"datasets": [{"name": "projects/p1/locations/us-central1/datasets/123",
			"labels": {"tofu-estate": "est"}}]}`))
	}))
	defer srv.Close()
	u, coll, err := ty.URL(ty.Root(srv.URL+"/v1/"), vars)
	require.NoError(t, err)
	objs, err := ty.List(context.Background(), srv.Client(), u, coll)
	require.NoError(t, err)
	require.Len(t, objs, 1)
	assert.Equal(t, "//aiplatform.googleapis.com/projects/p1/locations/us-central1/datasets/123", objs[0].AssetName)

	_, reason, ok := Lookup("google_dialogflow_cx_intent")
	assert.False(t, ok)
	assert.Contains(t, reason, "agent")
}

// TestHandwrittenTypesListAndNameByTheirTerraformID: Cloud SQL instances, GKE clusters and Assured Workloads
// workloads (INTENTIUS/plunge#19) list at their collection, read their labels where the API keeps them, and are
// named by their Terraform id.
func TestHandwrittenTypesListAndNameByTheirTerraformID(t *testing.T) {
	t.Parallel()
	vars := func(name string) (string, bool) {
		switch name {
		case "project":
			return "p1", true
		case "organization":
			return "123", true
		case "region", "location":
			return "us-central1", true
		}
		return "", false
	}
	for _, tc := range []struct {
		tfType, path, body, name string
		labels                   map[string]string
	}{
		{"google_sql_database_instance", "/sql/v1beta4/projects/p1/instances",
			`{"items": [{"name": "db1", "selfLink": "https://sqladmin.googleapis.com/sql/v1beta4/projects/p1/instances/db1",
			"settings": {"userLabels": {"plunge-stack": "s"}}}]}`,
			"projects/p1/instances/db1", map[string]string{"plunge-stack": "s"}},
		{"google_container_cluster", "/v1/projects/p1/locations/-/clusters",
			`{"clusters": [{"name": "c1", "location": "us-central1-a", "resourceLabels": {"plunge-stack": "s"}}]}`,
			"projects/p1/locations/us-central1-a/clusters/c1", map[string]string{"plunge-stack": "s"}},
		{"google_assured_workloads_workload", "/v1/organizations/123/locations/us-central1/workloads",
			`{"workloads": [{"name": "organizations/123/locations/us-central1/workloads/w1",
			"labels": {"plunge-stack": "s"}}]}`,
			"organizations/123/locations/us-central1/workloads/w1", map[string]string{"plunge-stack": "s"}},
	} {
		ty, reason, ok := Lookup(tc.tfType)
		require.True(t, ok, reason)
		assert.Empty(t, ty.Service.Consistency, "%s binds, consistency unmeasured", tc.tfType)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, tc.path, r.URL.Path, tc.tfType)
			_, _ = w.Write([]byte(tc.body))
		}))
		root := srv.URL + "/v1/"
		if tc.tfType == "google_sql_database_instance" {
			root = srv.URL + "/sql/v1beta4/"
		}
		u, coll, err := ty.URL(ty.Root(root), vars)
		require.NoError(t, err)
		objs, err := ty.List(context.Background(), srv.Client(), u, coll)
		srv.Close()
		require.NoError(t, err, tc.tfType)
		require.Len(t, objs, 1, tc.tfType)
		assert.Equal(t, tc.name, ty.ImportID(objs[0]), tc.tfType)
		assert.Equal(t, tc.labels, objs[0].Labels, tc.tfType)
	}
}
