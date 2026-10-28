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
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// versionedSpy is a graphSpy whose store keeps versions: it answers staterecord.VersionLister with a fixed listing
// per key, as an S3 store would, and counts the listings. It returns no payloads, because the listing has none.
type versionedSpy struct {
	*graphSpy
	mu       sync.Mutex
	listings map[string]int
	versions map[string][]staterecord.RecordVersion
	fail     map[string]error
}

func (s *versionedSpy) ListVersions(_ context.Context, key string) ([]staterecord.RecordVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listings[key]++
	if err := s.fail[key]; err != nil {
		return nil, err
	}
	return s.versions[key], nil
}

var _ staterecord.VersionLister = (*versionedSpy)(nil)

func emitWithVersions(t *testing.T, store staterecord.Store) (*GraphDocument, []byte) {
	t.Helper()
	be := newBackend(nil, "plunge+file:///unused", nil, store)
	be.newListers = func() map[Family]Lister {
		t.Fatal("plunge graph --versions built the bind listers: it must make no cloud or provider call")
		return nil
	}
	doc, err := EmitGraph(context.Background(), be, graphRef{"app", "dev"}, GraphRequest{
		EngineVersion: "3.272.0-plunge.1", Now: graphNow, Versions: true,
	})
	require.NoError(t, err)
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	return doc, raw
}

func nodeByID(doc *GraphDocument, urn resource.URN) *GraphNode {
	for i := range doc.Nodes {
		if doc.Nodes[i].ID == string(urn) {
			return &doc.Nodes[i]
		}
	}
	return nil
}

// --versions lists each record's versions through staterecord's VersionLister, newest first, with no record value
// and no secret in the output, and still validates against chant's schema.
func TestGraphVersionsListsWithoutValues(t *testing.T) {
	t.Parallel()
	spy := newGraphSpy(t)
	seedGraphStack(t, spy.Store)
	t0 := time.Date(2026, 10, 26, 11, 0, 0, 0, time.UTC)
	vs := &versionedSpy{
		graphSpy: spy,
		listings: map[string]int{},
		versions: map[string][]staterecord.RecordVersion{
			graphKeys.resource(gBucketURN): {
				{VersionID: "v3", LastModified: t0.Add(40 * time.Minute), Current: true},
				{VersionID: "v2", LastModified: t0.Add(20 * time.Minute)},
				{VersionID: "v1", LastModified: t0},
			},
		},
		fail: map[string]error{graphKeys.resource(gK8sURN): errors.New("AccessDenied: s3:ListBucketVersions")},
	}

	doc, raw := emitWithVersions(t, vs)
	validateGraph(t, compileGraphSchema(t), raw)

	bucket := nodeByID(doc, gBucketURN)
	require.NotNil(t, bucket)
	assert.Equal(t, &GraphRecordVersions{Kept: true, Versions: []GraphRecordVersion{
		{ID: "v3", WrittenAt: "2026-10-26T11:40:00Z", Current: true},
		{ID: "v2", WrittenAt: "2026-10-26T11:20:00Z"},
		{ID: "v1", WrittenAt: "2026-10-26T11:00:00Z"},
	}}, bucket.Facts.RecordVersions)

	// A record with no versions listed is kept, with none.
	gcp := nodeByID(doc, gGCPURN)
	assert.Equal(t, &GraphRecordVersions{Kept: true, Versions: []GraphRecordVersion{}}, gcp.Facts.RecordVersions)

	// A listing the store refused leaves the node's versions unknown and says so in gaps.
	k8s := nodeByID(doc, gK8sURN)
	assert.Nil(t, k8s.Facts.RecordVersions)
	assert.Contains(t, doc.Meta.EngineGraph.Gaps, GraphGap{
		Address: string(gK8sURN), Reason: "versions-unlisted", Detail: "AccessDenied: s3:ListBucketVersions",
	})

	// One listing per node, the one bulk read, and no keyed read: no version's value is fetched.
	assert.Len(t, vs.listings, len(doc.Nodes))
	for key, n := range vs.listings {
		assert.Equal(t, 1, n, key)
	}
	assert.Equal(t, 1, spy.getAlls)
	assert.Zero(t, spy.gets)
	assert.Zero(t, spy.writes)

	// No record value reaches the output: no input, no output value, no secret, no ciphertext.
	out := string(raw)
	assert.NotContains(t, out, gSecretWord)
	assert.NotContains(t, out, gSecretText)
	assert.NotContains(t, out, "ciphertext")
	assert.NotContains(t, out, resourceFormat, "no record payload")
	for _, v := range []string{gSecretWord, gSecretText} {
		assert.NotContains(t, out, storedCiphertext(t, resource.NewProperty(v)))
	}
}

// A store that replaces records in place (here the local store) reports kept false and no versions on every node.
func TestGraphVersionsUnkeptStore(t *testing.T) {
	t.Parallel()
	spy := newGraphSpy(t)
	seedGraphStack(t, spy.Store)

	doc, raw := emitWithVersions(t, spy)
	validateGraph(t, compileGraphSchema(t), raw)
	require.NotEmpty(t, doc.Nodes)
	for _, n := range doc.Nodes {
		assert.Equal(t, &GraphRecordVersions{Kept: false, Versions: []GraphRecordVersion{}}, n.Facts.RecordVersions, n.ID)
	}
	assert.Empty(t, doc.Meta.EngineGraph.Gaps)
	assert.NotContains(t, string(raw), gSecretWord)
}
