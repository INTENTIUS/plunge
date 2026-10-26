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

// The acceptance tests of INTENTIUS/plunge#15 (marker binding gaps):
//
//   - a pulumi-aws type with a two-part Cloud Control identifier is bound by marker, its identifier translated to
//     the Terraform id;
//   - a covered GCP type is listed through its own service, which carries the collision claim, and an uncovered one
//     falls back to Asset Inventory, which the ownership entry says;
//   - azure-native marker placement reads the provider schema;
//   - marker policy and binder diagnostics arrive as engine diagnostic events.
//
// Written for epic:engine-ship and not run in this unit; the maintainer runs them.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/gcplist"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/engine"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// mappingProvider is a bridged provider serving mapping as its GetMapping("terraform").
func mappingProvider(pkg, version, mapping string) *deploytest.Provider {
	return &deploytest.Provider{
		Version: semver.MustParse(version),
		GetMappingF: func(_ context.Context, req plugin.GetMappingRequest) (plugin.GetMappingResponse, error) {
			if req.Key != "terraform" {
				return plugin.GetMappingResponse{}, nil
			}
			return plugin.GetMappingResponse{Provider: pkg, Data: []byte(mapping)}, nil
		},
	}
}

func markedWith(t *testing.T, urn resource.URN, enc markers.Encoding, extra map[string]string) map[string]string {
	t.Helper()
	want, err := markers.For(urn, enc)
	require.NoError(t, err)
	out := map[string]string{}
	for k, v := range extra {
		out[k] = v
	}
	for k, v := range want {
		out[k] = v
	}
	return out
}

const keyspacesType = "aws:keyspaces/table:Table"

const keyspacesMapping = `{
  "provider": {"resources": {
    "aws_keyspaces_table": {"keyspace_name": {"type": 4, "required": true}, "table_name": {"type": 4, "required": true},
      "tags": {"type": 6, "optional": true, "element": {"schema": {"type": 4}}}}
  }},
  "resources": {"aws_keyspaces_table": {"tok": "aws:keyspaces/table:Table", "fields": {}}}
}`

// A Cloud Control identifier of two parts ("KeyspaceName|TableName") becomes the Terraform id pkg/identity's row
// spells ("keyspace_name/table_name"), and the create binds to it by marker.
func TestMultiPartCloudControlIdentifierBindsByMarker(t *testing.T) {
	urn := resource.NewURN("test", "test", "", keyspacesType, "events")
	tags := markedWith(t, urn, markers.EncodingTags, map[string]string{"team": "a"})

	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct{ TypeName string }
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		asked = append(asked, r.Header.Get("X-Amz-Target")+" "+req.TypeName)
		mu.Unlock()
		var tagList []map[string]string
		for k, v := range tags {
			tagList = append(tagList, map[string]string{"Key": k, "Value": v})
		}
		props, _ := json.Marshal(map[string]any{"KeyspaceName": "ks", "TableName": "events", "Tags": tagList})
		other, _ := json.Marshal(map[string]any{"KeyspaceName": "ks", "TableName": "other", "Tags": []any{}})
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []map[string]string{
			{"Identifier": "ks|events", "Properties": string(props)},
			{"Identifier": "ks|other", "Properties": string(other)},
		}})
	}))
	defer srv.Close()
	t.Setenv(EnvCloudControlEndpoint, srv.URL)
	t.Setenv("AWS_REGION", "us-east-1")

	prov := mappingProvider("aws", "7.0.0", keyspacesMapping)
	cat := newCatalog()
	info := cat.lookup(context.Background(), prov, keyspacesType)
	require.Equal(t, "AWS::Cassandra::Table", info.CFNType)
	ok, reason := (&cloudControlLister{}).Covers(info)
	require.True(t, ok, reason)

	inputs, merged := mergeMarkers(resource.NewPropertyMapFromMap(map[string]any{
		"keyspaceName": "ks", "tableName": "events", "tags": map[string]any{"team": "a"},
	}), info, markedWith(t, urn, markers.EncodingTags, nil))
	require.True(t, merged)
	marks := newLedger()
	b := newBinder(cat, marks, map[Family]Lister{FamilyAWS: &cloudControlLister{}})
	id, err := b.Bind(context.Background(), deploy.BindRequest{URN: urn, Type: keyspacesType, Provider: prov, Inputs: inputs})
	require.NoError(t, err)
	assert.Equal(t, resource.ID("ks/events"), id, "the Terraform id, not the Cloud Control identifier")
	assert.Equal(t, []string{"CloudApiService.ListResources AWS::Cassandra::Table"}, asked)
	e, _ := marks.get(urn)
	assert.Equal(t, resource.ID("ks/events"), e.Bound)
	assert.Empty(t, e.Unlisted)
}

// The translation follows each row's own separators and order, and refuses a row it cannot follow.
func TestCloudControlIdentifierTranslation(t *testing.T) {
	t.Parallel()

	// aws_timestreamwrite_table's id is TABLE_NAME:DATABASE_NAME, the reverse of its primary identifier's order.
	tr, reason := ccTerraformID("aws_timestreamwrite_table", "AWS::Timestream::Table", []string{"DatabaseName", "TableName"})
	require.Empty(t, reason)
	id, ok := tr.apply("db|tbl")
	require.True(t, ok)
	assert.Equal(t, "tbl:db", id)
	_, ok = tr.apply("db")
	assert.False(t, ok, "an identifier with the wrong number of parts is not translated")

	// A rename: AWS::Kendra::Faq's Id is the faq_id argument.
	tr, reason = ccTerraformID("aws_kendra_faq", "AWS::Kendra::Faq", []string{"Id", "IndexId"})
	require.Empty(t, reason)
	id, _ = tr.apply("f-1|i-1")
	assert.Equal(t, "f-1/i-1", id)

	// A server-assigned row has no grammar to translate into: the type stays unlisted, with the reason.
	_, reason = ccTerraformID("aws_guardduty_ipset", "AWS::GuardDuty::IPSet", []string{"Id", "DetectorId"})
	assert.NotEmpty(t, reason)

	assert.Equal(t, "vpc_connection_id", snakeCase("VPCConnectionId"))
	assert.Equal(t, "keyspace_name", snakeCase("KeyspaceName"))
}

const gcpMapping = `{
  "provider": {"resources": {
    "google_pubsub_topic": {"name": {"type": 4, "required": true}, "labels": {"type": 6, "optional": true, "element": {"schema": {"type": 4}}}}
  }},
  "resources": {"google_pubsub_topic": {"tok": "gcp:pubsub/topic:Topic", "fields": {}}}
}`

// A covered GCP type is listed through its own service (Pub/Sub's projects/<p>/topics), two marked topics are a
// collision naming both, one is bound, and the ownership entry names the listing and says its consistency is
// unmeasured.
func TestGCPCoveredTypeListsThroughItsService(t *testing.T) {
	t.Parallel()

	urn := resource.NewURN("test", "test", "", "gcp:pubsub/topic:Topic", "events")
	labels := markedWith(t, urn, markers.EncodingLabels, map[string]string{"team": "a"})
	var mu sync.Mutex
	topics := []map[string]any{{"name": "projects/plc/topics/events", "labels": labels}}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.URL.Path)
		assert.Equal(t, "Bearer fake-token", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{"topics": topics})
	}))
	defer srv.Close()

	prov := mappingProvider("gcp", "10.2.0", gcpMapping)
	cat := newCatalog()
	info := cat.lookup(context.Background(), prov, "gcp:pubsub/topic:Topic")
	require.Equal(t, "google_pubsub_topic", info.TFType)
	_, _, covered := gcplist.Lookup(info.TFType)
	require.True(t, covered)

	inputs, merged := mergeMarkers(resource.NewPropertyMapFromMap(map[string]any{
		"name": "events", "labels": map[string]any{"team": "a"},
	}), info, markedWith(t, urn, markers.EncodingLabels, nil))
	require.True(t, merged)
	req := deploy.BindRequest{
		URN: urn, Type: "gcp:pubsub/topic:Topic", Provider: prov, Inputs: inputs,
		ProviderInputs: resource.NewPropertyMapFromMap(map[string]any{
			"project": "plc", "accessToken": "fake-token", "pubsubCustomEndpoint": srv.URL + "/v1/",
		}),
	}

	marks := newLedger()
	b := newBinder(cat, marks, map[Family]Lister{FamilyGoogle: newGCPLister()})
	id, err := b.Bind(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, resource.ID("projects/plc/topics/events"), id)
	e, _ := marks.get(urn)
	// Only Cloud Storage's listing carries the collision claim (INTENTIUS/plunge#15, ruled 2026-10-26): Pub/Sub's
	// binds, and the record says its consistency is unmeasured.
	assert.Contains(t, e.Unlisted, "binds, consistency unmeasured")
	assert.Contains(t, e.Listing, "pubsub.googleapis.com")
	assert.Contains(t, e.Listing, "binds, consistency unmeasured")
	assert.Equal(t, []string{"/v1/projects/plc/topics"}, paths)

	mu.Lock()
	topics = append(topics, map[string]any{"name": "projects/plc/topics/events-copy", "labels": labels})
	mu.Unlock()
	_, err = b.Bind(context.Background(), req)
	var collision *CollisionError
	require.ErrorAs(t, err, &collision)
	assert.Equal(t, []resource.ID{"projects/plc/topics/events", "projects/plc/topics/events-copy"}, collision.IDs)
}

// A GCP type gcplist does not cover, or one whose list cannot be addressed, falls back to Asset Inventory, which
// binds without the collision claim, and says so.
func TestGCPUncoveredTypeFallsBackToAssetInventory(t *testing.T) {
	l := newGCPLister()
	ok, why := l.ConsistentFor(TypeInfo{Type: "gcp:x/y:Y", Family: FamilyGoogle, TFType: "google_not_a_type"},
		deploy.BindRequest{})
	assert.False(t, ok)
	assert.Contains(t, why, "Cloud Asset Inventory")

	// Covered, but no project anywhere: the list cannot be addressed.
	t.Setenv("GOOGLE_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("CLOUDSDK_CORE_PROJECT", "")
	ok, why = l.ConsistentFor(TypeInfo{Type: "gcp:pubsub/topic:Topic", Family: FamilyGoogle,
		TFType: "google_pubsub_topic"}, deploy.BindRequest{})
	assert.False(t, ok)
	assert.Contains(t, why, "project")
	assert.Contains(t, l.Listing(TypeInfo{TFType: "google_pubsub_topic"}, deploy.BindRequest{}), "Asset Inventory")
}

// The Compute Engine API names its objects bare, and the selfLink carries the Asset path.
func TestGCPComputeObjectsAreNamedFromTheirSelfLink(t *testing.T) {
	t.Parallel()

	ty, reason, ok := gcplist.Lookup("google_compute_disk")
	require.True(t, ok, reason)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/compute/v1/projects/plc/zones/us-central1-a/disks", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{
			"name":     "d1",
			"selfLink": "https://www.googleapis.com/compute/v1/projects/plc/zones/us-central1-a/disks/d1",
			"labels":   map[string]string{"a": "b"},
		}}})
	}))
	defer srv.Close()
	vars := map[string]string{"project": "plc", "zone": "us-central1-a"}
	u, coll, err := ty.URL(ty.Root(srv.URL+"/compute/v1/"), func(n string) (string, bool) {
		v, ok := vars[n]
		return v, ok
	})
	require.NoError(t, err)
	objs, err := ty.List(context.Background(), srv.Client(), u, coll)
	require.NoError(t, err)
	require.Len(t, objs, 1)
	assert.Equal(t, "projects/plc/zones/us-central1-a/disks/d1", objs[0].Name)
	assert.Equal(t, "projects/plc/zones/us-central1-a/disks/d1", ty.ImportID(objs[0]))
}

const azureNativeSchema = `{
  "name": "azure-native",
  "resources": {
    "azure-native:storage:StorageAccount": {"inputProperties": {
      "accountName": {"type": "string"},
      "tags": {"type": "object", "additionalProperties": {"type": "string"}}
    }},
    "azure-native:authorization:RoleAssignment": {"inputProperties": {"principalId": {"type": "string"}}}
  }
}`

// azure-native marker placement reads the schema: a taggable type with no program tags is marked, and an
// untaggable one gets record-carried ownership.
func TestAzureNativeMarkerPlacementReadsTheSchema(t *testing.T) {
	t.Parallel()

	reads := 0
	prov := &deploytest.Provider{
		Version: semver.MustParse("3.5.0"),
		GetSchemaF: func(context.Context, plugin.GetSchemaRequest) (plugin.GetSchemaResponse, error) {
			reads++
			return plugin.GetSchemaResponse{Schema: []byte(azureNativeSchema)}, nil
		},
	}
	cat := newCatalog()
	cat.azureNativeTags = &azureNativeLoader{versions: map[string]*azureNativeTags{}, failed: map[string]error{}}
	marks := newLedger()
	pol := newMarkerPolicy(cat, nil, marks, nil)

	account := resource.NewURN("test", "test", "", "azure-native:storage:StorageAccount", "logs")
	out, err := pol.Inject(context.Background(), deploy.MarkerRequest{
		URN: account, Type: "azure-native:storage:StorageAccount", Provider: prov,
		Properties: resource.NewPropertyMapFromMap(map[string]any{"accountName": "logs"}),
	})
	require.NoError(t, err)
	tags := stringTags(out["tags"])
	for k, v := range markedWith(t, account, markers.EncodingTags, nil) {
		assert.Equal(t, v, tags[k], k)
	}
	e, _ := marks.get(account)
	assert.Equal(t, OwnershipMarker, e.By)
	assert.Equal(t, MarkerInjected, e.State)

	role := resource.NewURN("test", "test", "", "azure-native:authorization:RoleAssignment", "reader")
	out, err = pol.Inject(context.Background(), deploy.MarkerRequest{
		URN: role, Type: "azure-native:authorization:RoleAssignment", Provider: prov,
		Properties: resource.NewPropertyMapFromMap(map[string]any{"principalId": "p"}),
	})
	require.NoError(t, err)
	assert.NotContains(t, out, resource.PropertyKey("tags"))
	e, _ = marks.get(role)
	assert.Equal(t, OwnershipRecord, e.By)
	assert.Equal(t, MarkerUntaggable, e.State)

	assert.Equal(t, 1, reads, "the schema is read once per provider version")
}

const widgetType = "aws:m/widget:Widget"

const diagMapping = `{
  "provider": {"resources": {
    "aws_s3_bucket": {"bucket": {"type": 4, "optional": true}, "tags": {"type": 6, "optional": true, "element": {"schema": {"type": 4}}}},
    "aws_widget": {"tags": {"type": 5, "optional": true, "element": {"schema": {"type": 4}}}}
  }},
  "resources": {
    "aws_s3_bucket": {"tok": "aws:s3/bucket:Bucket", "fields": {}},
    "aws_widget": {"tok": "aws:m/widget:Widget", "fields": {}}
  }
}`

// The marker policy's and the binder's diagnostics (an unsupported tag shape, tags unknown at preview, a type with
// no lister) are read from the engine's event stream, as diagnostic events on each resource.
func TestMarkerDiagnosticsArriveAsEngineEvents(t *testing.T) {
	t.Parallel()

	loader := deploytest.NewProviderLoader("aws", semver.MustParse("7.0.0"), func() (plugin.Provider, error) {
		p := mappingProvider("aws", "7.0.0", diagMapping)
		p.CreateF = func(_ context.Context, req plugin.CreateRequest) (plugin.CreateResponse, error) {
			return plugin.CreateResponse{ID: resource.ID(req.URN.Name()), Properties: req.Properties,
				Status: resource.StatusOK}, nil
		}
		return p, nil
	})
	prog := deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		if _, err := monitor.RegisterResource(widgetType, "w", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{"tags": []any{"a"}}),
		}); err != nil {
			return err
		}
		if _, err := monitor.RegisterResource(bucketType, "unknown", true, deploytest.ResourceOptions{
			Inputs: resource.PropertyMap{"tags": resource.MakeComputed(resource.NewProperty(""))},
		}); err != nil {
			return err
		}
		_, err := monitor.RegisterResource(bucketType, "known", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{"tags": map[string]any{"team": "a"}}),
		})
		return err
	})

	events, err := run(t, newCountingStore(t), deploytest.NewPluginHostF(nil, nil, prog, nil, nil, loader),
		runConfig{markers: true, dryRun: true, listers: map[Family]Lister{}})
	require.NoError(t, err)

	byURN := map[string][]engine.DiagEventPayload{}
	for _, e := range events {
		if e.Type != engine.DiagEvent {
			continue
		}
		p, ok := e.Payload().(engine.DiagEventPayload)
		if ok && p.URN != "" {
			byURN[p.URN.Name()] = append(byURN[p.URN.Name()], p)
		}
	}
	has := func(name string, sev diag.Severity, text string) {
		for _, p := range byURN[name] {
			if p.Severity == sev && strings.Contains(p.Message, text) {
				return
			}
		}
		t.Errorf("no %s diagnostic event on %s containing %q; got %s", sev, name, text, fmt.Sprint(byURN[name]))
	}
	has("w", diag.Warning, "no ownership marker")
	has("unknown", diag.Warning, "not known yet")
	has("known", diag.Info, "no marker bind or collision check")
}

// The C2 claims (INTENTIUS/plunge#15, ruled 2026-10-26): Cloud Storage's list and Cloud Control's carry the collision
// claim; every other GCP service's list and ARM's bind with "binds, consistency unmeasured" as the coverage reason.
func TestCollisionClaimIsCloudStorageAndCloudControlOnly(t *testing.T) {
	t.Parallel()

	assert.True(t, (&cloudControlLister{}).Consistent())

	arm := &armLister{}
	assert.False(t, arm.Consistent())
	ok, why := arm.ConsistentFor(TypeInfo{Type: "azure-native:storage:StorageAccount", Family: FamilyAzureNative},
		deploy.BindRequest{})
	assert.False(t, ok)
	assert.Contains(t, why, "binds, consistency unmeasured")

	gcp := newGCPLister()
	prov := resource.NewPropertyMapFromMap(map[string]any{"project": "plc"})
	ok, why = gcp.ConsistentFor(TypeInfo{Type: "gcp:storage/bucket:Bucket", Family: FamilyGoogle,
		TFType: "google_storage_bucket"}, deploy.BindRequest{ProviderInputs: prov})
	assert.True(t, ok, why)
	assert.Contains(t, gcp.Listing(TypeInfo{TFType: "google_storage_bucket"},
		deploy.BindRequest{ProviderInputs: prov}), "strongly consistent")

	ok, why = gcp.ConsistentFor(TypeInfo{Type: "gcp:pubsub/topic:Topic", Family: FamilyGoogle,
		TFType: "google_pubsub_topic"}, deploy.BindRequest{ProviderInputs: prov})
	assert.False(t, ok)
	assert.Contains(t, why, "binds, consistency unmeasured")
	assert.Contains(t, why, "pubsub.googleapis.com")
}
