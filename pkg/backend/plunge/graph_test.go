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

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/pkg/v3/secrets/b64"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/sig"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// The schema copy and its source are in testdata/graph (see SOURCE there for the chant sha).
const graphSchemaID = "https://intentius.io/chant/schemas/graph/v1/graph.schema.json"

var graphKeys = newStackKeys("app", "dev")

const (
	gProvURN    = resource.URN("urn:pulumi:dev::app::pulumi:providers:aws::default")
	gProvID     = resource.ID("prov-1")
	gBucketURN  = resource.URN("urn:pulumi:dev::app::aws:s3/bucketV2:BucketV2::assets")
	gPolicyURN  = resource.URN("urn:pulumi:dev::app::aws:s3/bucketPolicy:BucketPolicy::assets-policy")
	gGCPURN     = resource.URN("urn:pulumi:dev::app::gcp:storage/bucket:Bucket::media")
	gK8sURN     = resource.URN("urn:pulumi:dev::app::kubernetes:core/v1:ConfigMap::cfg")
	gCompURN    = resource.URN("urn:pulumi:dev::app::my:index:Component::comp")
	gStackURN   = resource.URN("urn:pulumi:dev::app::pulumi:pulumi:Stack::app-dev")
	gSecretText = "arn:aws:s3:::the-secret-plaintext-0f3c"
	gSecretWord = "sekrit-token-value-77a1"
)

// graphSpy counts every call that reaches the store.
type graphSpy struct {
	staterecord.Store
	mu                                    sync.Mutex
	getAlls, gets, lists, writes, deletes int
	keysRead                              []string
}

func (s *graphSpy) GetAll(ctx context.Context, prefix string) (map[string]staterecord.Record, error) {
	s.mu.Lock()
	s.getAlls++
	s.mu.Unlock()
	return s.Store.(staterecord.BulkReader).GetAll(ctx, prefix)
}

func (s *graphSpy) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	s.mu.Lock()
	s.gets++
	s.keysRead = append(s.keysRead, key)
	s.mu.Unlock()
	return s.Store.Get(ctx, key)
}

func (s *graphSpy) List(ctx context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	s.lists++
	s.mu.Unlock()
	return s.Store.List(ctx, prefix)
}

func (s *graphSpy) PutIfVersion(ctx context.Context, key string, p []byte, v string) (string, error) {
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	return s.Store.PutIfVersion(ctx, key, p, v)
}

func (s *graphSpy) PutIfAbsent(ctx context.Context, key string, p []byte) (string, error) {
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	return s.Store.PutIfAbsent(ctx, key, p)
}

func (s *graphSpy) Delete(ctx context.Context, key string, v string) error {
	s.mu.Lock()
	s.deletes++
	s.mu.Unlock()
	return s.Store.Delete(ctx, key, v)
}

type graphRef struct{ project, stack string }

func (r graphRef) String() string               { return r.project + "/" + r.stack }
func (r graphRef) Name() tokens.StackName       { return tokens.MustParseStackName(r.stack) }
func (r graphRef) Project() (tokens.Name, bool) { return tokens.Name(r.project), true }
func (r graphRef) FullyQualifiedName() tokens.QName {
	return tokens.QName("organization/" + r.String())
}

func putGraphRecord(t *testing.T, store staterecord.Store, r resourceRecord) {
	t.Helper()
	r.Format = resourceFormat
	payload, err := json.Marshal(r)
	require.NoError(t, err)
	_, err = store.PutIfAbsent(context.Background(), graphKeys.resource(r.URN), payload)
	require.NoError(t, err)
}

func serializeOutputs(t *testing.T, m resource.PropertyMap) map[string]any {
	t.Helper()
	out, err := stack.SerializeProperties(context.Background(), m, b64.NewBase64SecretsManager().Encrypter(), false)
	require.NoError(t, err)
	return out
}

// seedGraphStack writes a small stack's records: a provider, a tagged bucket bound by its markers, a policy with a
// pending create and a property dependency on the bucket, a GCP bucket with digest markers, a Kubernetes object, a
// component, the root Stack, a secrets record and a stack output.
func seedGraphStack(t *testing.T, store staterecord.Store) {
	t.Helper()
	provRef := string(gProvURN) + "::" + string(gProvID)

	putGraphRecord(t, store, resourceRecord{URN: gStackURN, States: []apitype.ResourceV3{{
		URN: gStackURN, Type: resource.RootStackType,
	}}})
	putGraphRecord(t, store, resourceRecord{URN: gProvURN, States: []apitype.ResourceV3{{
		URN: gProvURN, Custom: true, ID: gProvID, Type: "pulumi:providers:aws", Parent: gStackURN,
	}}, Ownership: &ownershipEntry{By: OwnershipRecord, State: MarkerUntaggable}})

	tagMarks, err := markers.For(gBucketURN, markers.EncodingTags)
	require.NoError(t, err)
	putGraphRecord(t, store, resourceRecord{URN: gBucketURN, States: []apitype.ResourceV3{{
		URN: gBucketURN, Custom: true, ID: "assets-4f1c2e9", Type: "aws:s3/bucketV2:BucketV2",
		Parent: gCompURN, Provider: provRef,
		Inputs: map[string]any{"bucket": "assets-4f1c2e9", "password": gSecretWord},
		Outputs: serializeOutputs(t, resource.PropertyMap{
			"arn":    resource.NewProperty("arn:aws:s3:::assets-4f1c2e9"),
			"bucket": resource.NewProperty("assets-4f1c2e9"),
			"token":  resource.MakeSecret(resource.NewProperty(gSecretWord)),
		}),
	}}, Ownership: &ownershipEntry{By: OwnershipMarker, State: MarkerInjected, Family: FamilyAWS, Marker: tagMarks}})

	putGraphRecord(t, store, resourceRecord{URN: gPolicyURN, Pending: []apitype.OperationV2{{
		Type: apitype.OperationTypeCreating,
		Resource: apitype.ResourceV3{
			URN: gPolicyURN, Custom: true, Type: "aws:s3/bucketPolicy:BucketPolicy", Parent: gStackURN,
			Provider: provRef, Dependencies: []resource.URN{gBucketURN},
			PropertyDependencies: map[resource.PropertyKey][]resource.URN{"bucket": {gBucketURN}},
		},
	}}, Ownership: &ownershipEntry{By: OwnershipRecord}})

	labelMarks, err := markers.For(gGCPURN, markers.EncodingLabels)
	require.NoError(t, err)
	putGraphRecord(t, store, resourceRecord{URN: gGCPURN, States: []apitype.ResourceV3{{
		URN: gGCPURN, Custom: true, ID: "media-9a0b", Type: "gcp:storage/bucket:Bucket", Parent: gStackURN,
		Outputs: serializeOutputs(t, resource.PropertyMap{
			"id":       resource.NewProperty("media-9a0b"),
			"selfLink": resource.MakeSecret(resource.NewProperty(gSecretText)),
		}),
	}}, Ownership: &ownershipEntry{By: OwnershipMarker, Family: FamilyGoogle, Marker: labelMarks}})

	putGraphRecord(t, store, resourceRecord{URN: gK8sURN, States: []apitype.ResourceV3{{
		URN: gK8sURN, Custom: true, ID: "default/cfg", Type: "kubernetes:core/v1:ConfigMap", Parent: gStackURN,
		Outputs: serializeOutputs(t, resource.PropertyMap{
			"metadata": resource.NewProperty(resource.PropertyMap{
				"uid": resource.NewProperty("7c1e2d3f-uid"),
			}),
			"data": resource.MakeSecret(resource.NewProperty(resource.PropertyMap{
				"key": resource.NewProperty(gSecretWord),
			})),
		}),
	}}})

	putGraphRecord(t, store, resourceRecord{URN: gCompURN, States: []apitype.ResourceV3{{
		URN: gCompURN, Type: "my:index:Component", Parent: gStackURN,
	}}})

	ctx := context.Background()
	_, err = store.PutIfAbsent(ctx, graphKeys.secrets(), []byte(`{"format":"`+secretsFormat+`","type":"b64"}`))
	require.NoError(t, err)
	out := serializeOutputs(t, resource.PropertyMap{"pw": resource.MakeSecret(resource.NewProperty(gSecretWord))})
	payload, err := json.Marshal(outputRecord{Format: outputFormat, Name: "pw", Value: out["pw"]})
	require.NoError(t, err)
	_, err = store.PutIfAbsent(ctx, graphKeys.output("pw"), payload)
	require.NoError(t, err)
}

func newGraphSpy(t *testing.T) *graphSpy {
	local, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	return &graphSpy{Store: local}
}

var graphNow = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }

func emitSeeded(t *testing.T) (*GraphDocument, []byte, *graphSpy) {
	t.Helper()
	spy := newGraphSpy(t)
	seedGraphStack(t, spy.Store)
	be := newBackend(nil, "plunge+file:///unused", nil, spy)
	be.newListers = func() map[Family]Lister {
		t.Fatal("plunge graph built the bind listers: it must make no cloud or provider call")
		return nil
	}
	doc, err := EmitGraph(context.Background(), be, graphRef{"app", "dev"}, GraphRequest{
		EngineVersion: "3.272.0-plunge.1", Now: graphNow,
	})
	require.NoError(t, err)
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	return doc, raw, spy
}

func compileGraphSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "graph", "graph.schema.json"))
	require.NoError(t, err)
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	require.NoError(t, c.AddResource(graphSchemaID, bytes.NewReader(b)))
	s, err := c.Compile(graphSchemaID + "#/$defs/EngineGraphDocument")
	require.NoError(t, err)
	return s
}

func validateGraph(t *testing.T, s *jsonschema.Schema, raw []byte) {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal(raw, &v))
	require.NoError(t, s.Validate(v))
}

// The emitted document validates against chant#4527's schema, as chant's own plunge fixture does, and decodes back
// into the emitter's types with no field left over.
func TestGraphValidatesAgainstChantSchema(t *testing.T) {
	t.Parallel()
	s := compileGraphSchema(t)

	fixture, err := os.ReadFile(filepath.Join("testdata", "graph", "plunge.engine-graph.json"))
	require.NoError(t, err)
	validateGraph(t, s, fixture)
	var fromFixture GraphDocument
	dec := json.NewDecoder(bytes.NewReader(fixture))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&fromFixture), "chant's plunge fixture has a field the emitter's types do not")

	doc, raw, _ := emitSeeded(t)
	validateGraph(t, s, raw)

	eg := doc.Meta.EngineGraph
	assert.Equal(t, 1, doc.Version)
	assert.Equal(t, GraphScope{Kind: "stack", Project: "app", Stack: "dev"}, eg.Scope)
	assert.Equal(t, "2026-10-26T12:00:00Z", eg.ObservedAt)
	assert.Equal(t, "plunge-stack", eg.MarkerScheme.OwnerKey)
	assert.Equal(t, "plunge-urn", eg.MarkerScheme.AddressKey)
	assert.Equal(t, []string{"plunge-urn-2", "plunge-urn-3", "plunge-urn-4"}, eg.MarkerScheme.ContinuationKeys)
	assert.Equal(t, GraphEncoding{Tags: "plain", Labels: "digest"}, eg.MarkerScheme.Encoding)
	assert.Empty(t, eg.Gaps)
	assert.Len(t, doc.Nodes, 7)
	for _, n := range doc.Nodes {
		assert.True(t, strings.HasPrefix(n.ID, "urn:pulumi:dev::app::"), "node ids are full URNs: %s", n.ID)
		assert.Equal(t, map[string]string{"address": n.ID}, n.Attrs, "attrs carry the address only")
	}
}

// A secret output never reaches the document, nor any input or ciphertext: the emitter reads only the ID-shaped
// outputs and skips a secret one without decrypting it. Red first: have cloudIDs read through the secret envelope
// (or copy inputs or outputs onto attrs) and this fails.
func TestGraphNeverPrintsSecrets(t *testing.T) {
	t.Parallel()
	_, raw, spy := emitSeeded(t)
	out := string(raw)

	assert.NotContains(t, out, gSecretWord)
	assert.NotContains(t, out, gSecretText)
	assert.NotContains(t, out, "ciphertext")
	assert.NotContains(t, out, "1b47061264138c4ac30d75fd1eb44270", "no secret signature")
	secrets := []resource.PropertyValue{
		resource.NewProperty(gSecretWord),
		resource.NewProperty(gSecretText),
		resource.NewProperty(resource.PropertyMap{"key": resource.NewProperty(gSecretWord)}),
	}
	for _, v := range secrets {
		ct := storedCiphertext(t, v)
		assert.NotContains(t, out, ct, "the ciphertext the state holds for %v", v)
	}
	// The secrets record is never read: nothing is decrypted.
	assert.NotContains(t, spy.keysRead, graphKeys.secrets())
}

// storedCiphertext is the ciphertext a record holds for v made secret: the one in the envelope serializeOutputs writes,
// which encrypts v's JSON encoding (a string's quotes included), so it is never the ciphertext of the bare string.
func storedCiphertext(t *testing.T, v resource.PropertyValue) string {
	t.Helper()
	out := serializeOutputs(t, resource.PropertyMap{"v": resource.MakeSecret(v)})
	// The record holds the envelope as JSON; read it back the way the record's reader does.
	raw, err := json.Marshal(out["v"])
	require.NoError(t, err)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.Equal(t, sig.Secret, envelope[sig.Key], "a secret serializes to an envelope")
	ct, ok := envelope["ciphertext"].(string)
	require.True(t, ok, "the envelope holds a ciphertext")
	require.NotEmpty(t, ct)
	return ct
}

// The emit is one bulk read and nothing else: no keyed read, no listing, no write, no delete, and no lister or
// provider (newListers fails the test if it is built; EmitGraph takes no plugin host).
func TestGraphOneBulkReadNoProviderCall(t *testing.T) {
	t.Parallel()
	_, _, spy := emitSeeded(t)
	assert.Equal(t, 1, spy.getAlls, "one bulk read")
	assert.Zero(t, spy.gets, "no keyed read")
	assert.Zero(t, spy.lists, "no listing")
	assert.Zero(t, spy.writes, "read-only: no write")
	assert.Zero(t, spy.deletes, "read-only: no delete")
}

// Dependencies, parents and providers are instance-grain edges; each property dependency is a property-grain edge
// named by its property.
func TestGraphEdgeGrains(t *testing.T) {
	t.Parallel()
	doc, _, _ := emitSeeded(t)
	has := func(from, to resource.URN, via, grain string) bool {
		for _, e := range doc.Edges {
			if e.From == string(from) && e.To == string(to) && e.ViaAttr == via && e.Grain == grain && e.Kind == "ref" {
				return true
			}
		}
		return false
	}
	assert.True(t, has(gPolicyURN, gBucketURN, ViaDependencies, GrainInstance))
	assert.True(t, has(gPolicyURN, gBucketURN, "bucket", GrainProperty))
	assert.True(t, has(gPolicyURN, gProvURN, ViaProvider, GrainInstance))
	assert.True(t, has(gBucketURN, gCompURN, ViaParent, GrainInstance))
	assert.True(t, has(gBucketURN, gProvURN, ViaProvider, GrainInstance))
	assert.True(t, has(gCompURN, gStackURN, ViaParent, GrainInstance))
	for _, e := range doc.Edges {
		assert.Contains(t, []string{GrainInstance, GrainProperty}, e.Grain)
		if e.Grain == GrainProperty {
			assert.NotContains(t, []string{ViaDependencies, ViaParent, ViaProvider}, e.ViaAttr)
		}
	}
}

// Ownership is bound for a recorded state and pending for a pending create, never unowned or foreign; ownedBy is
// the ownership entry's By; markers come from the entry with source "record" and the family's encoding; cloudIds are
// the state's ID plus the family's non-secret ID-shaped outputs.
func TestGraphOwnershipMapping(t *testing.T) {
	t.Parallel()
	doc, _, _ := emitSeeded(t)
	nodes := map[string]GraphNode{}
	for _, n := range doc.Nodes {
		nodes[n.ID] = n
		if f := n.Facts; f != nil {
			assert.Contains(t, []string{"", "bound", "pending"}, f.Ownership, n.ID)
			assert.Contains(t, []string{"", "marker", "record"}, f.OwnedBy, n.ID)
			assert.Nil(t, f.RecordVersions, "recordVersions only with --versions")
			if f.Markers != nil {
				assert.Equal(t, "record", f.Markers.Source)
			}
		}
	}

	bucket := nodes[string(gBucketURN)]
	assert.Equal(t, "bound", bucket.Facts.Ownership)
	assert.Equal(t, "marker", bucket.Facts.OwnedBy)
	assert.Equal(t, "assets-4f1c2e9", bucket.PhysicalID)
	assert.Equal(t, &GraphMarkers{
		Owner: "app/dev", Address: "aws:s3/bucketV2:BucketV2::assets", Encoding: "plain", Source: "record",
	}, bucket.Facts.Markers)
	assert.Equal(t, []GraphCloudID{
		{Kind: "provider-id", Value: "assets-4f1c2e9"},
		{Kind: "arn", Value: "arn:aws:s3:::assets-4f1c2e9"},
	}, bucket.Facts.CloudIDs)

	policy := nodes[string(gPolicyURN)]
	assert.Equal(t, "pending", policy.Facts.Ownership)
	assert.Equal(t, "record", policy.Facts.OwnedBy)
	assert.Empty(t, policy.PhysicalID)
	assert.Nil(t, policy.Facts.Markers)
	assert.Empty(t, policy.Facts.CloudIDs)

	gcp := nodes[string(gGCPURN)]
	assert.Equal(t, "digest", gcp.Facts.Markers.Encoding)
	assert.Len(t, gcp.Facts.Markers.Owner, 16)
	assert.Len(t, gcp.Facts.Markers.Address, 32)
	// The id output equals the state's ID and the selfLink is a secret: only the primary ID remains.
	assert.Equal(t, []GraphCloudID{{Kind: "provider-id", Value: "media-9a0b"}}, gcp.Facts.CloudIDs)

	k8s := nodes[string(gK8sURN)]
	assert.Equal(t, "bound", k8s.Facts.Ownership)
	assert.Empty(t, k8s.Facts.OwnedBy, "a record with no ownership entry says nothing about how it is owned")
	assert.Equal(t, []GraphCloudID{
		{Kind: "provider-id", Value: "default/cfg"},
		{Kind: "k8s-uid", Value: "7c1e2d3f-uid"},
	}, k8s.Facts.CloudIDs)

	prov := nodes[string(gProvURN)]
	assert.Equal(t, "bound", prov.Facts.Ownership)
	assert.Equal(t, "record", prov.Facts.OwnedBy)
	assert.Nil(t, prov.Facts.Markers)

	comp := nodes[string(gCompURN)]
	assert.Empty(t, comp.Facts.Ownership, "a component has no cloud object to bind")
	assert.Empty(t, comp.PhysicalID)
}
