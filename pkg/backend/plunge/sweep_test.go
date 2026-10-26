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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// Two runs race to create the bucket and both creates land, as logs-1 and logs-2. Both read the records before
// either wrote, so the second record write is refused, and the record names only the winner's object. The sweep
// after the losing run lists the bucket type with the stack's marker and reports logs-2, by ID and with the URN its
// markers name, as an unrecorded marked object. It deletes nothing, and the winner's record is left as it was.
func TestSweepReportsTheObjectOfARacesLostRecordWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newCountingStore(t)
	urn := bucketURN()
	info := TypeInfo{Type: tokens.Type(bucketType), Family: FamilyAWS, Property: "tags", Shape: ShapeMap,
		TFType: "aws_s3_bucket"}

	want, err := markers.For(urn, markers.EncodingTags)
	require.NoError(t, err)
	tags := map[string]string{"team": "a"}
	for k, v := range want {
		tags[k] = v
	}
	state := func(id string) apitype.ResourceV3 {
		tagsAny := map[string]any{}
		for k, v := range tags {
			tagsAny[k] = v
		}
		return apitype.ResourceV3{
			URN: urn, Type: tokens.Type(bucketType), Custom: true, ID: resource.ID(id),
			Inputs:  map[string]any{"bucket": "logs", "tags": tagsAny},
			Outputs: map[string]any{"bucket": "logs", "tags": tagsAny},
		}
	}

	// Both runs read the empty records, then each creates its own object.
	lA, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	lB, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	winner := newRecordPersister(ctx, store, lA, Generation{}, true, nil)
	require.NoError(t, winner.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
		Resources: []apitype.ResourceV3{state("logs-1")},
	}}))
	loser := newRecordPersister(ctx, store, lB, Generation{}, true, nil)
	err = loser.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
		Resources: []apitype.ResourceV3{state("logs-2")},
	}})
	var conflict *RecordConflictError
	require.ErrorAs(t, err, &conflict, "the second record write of the race loses")

	// The cloud holds both objects; the losing run marked the bucket.
	cloud := &fakeLister{objs: []LiveObject{{ID: "logs-1", Tags: tags}, {ID: "logs-2", Tags: tags}}}
	marks := newLedger()
	marks.noteMarked(urn, info, nil)

	var out bytes.Buffer
	sink := diag.DefaultSink(&out, &out, diag.FormatOptions{Color: colors.Never})
	found := reportUnrecorded(ctx, sink, store, testKeys, marks, map[Family]Lister{FamilyAWS: cloud})
	require.Len(t, found, 1)
	assert.Equal(t, resource.ID("logs-2"), found[0].ID)
	assert.Equal(t, tokens.Type(bucketType), found[0].Type)
	assert.Equal(t, urn, found[0].Claims)
	assert.Contains(t, out.String(), "unrecorded marked object")
	assert.Contains(t, out.String(), "logs-2")
	assert.NotContains(t, out.String(), "logs-1 carries")

	// Nothing was deleted or rewritten: the record still names the winner's object.
	l, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	require.Len(t, l.deployment.Deployment.Resources, 1)
	assert.Equal(t, resource.ID("logs-1"), l.deployment.Deployment.Resources[0].ID)
	assert.Len(t, cloud.objs, 2)
}

// A marked object the records name is not reported, a listing whose consistency is unmeasured is swept, and a type
// listed through Cloud Asset Inventory is not.
func TestSweepSkipsRecordedObjectsAndIndexListings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newCountingStore(t)
	urn := bucketURN()
	info := TypeInfo{Type: tokens.Type(bucketType), Family: FamilyAWS, Property: "tags", Shape: ShapeMap}
	want, err := markers.For(urn, markers.EncodingTags)
	require.NoError(t, err)

	l, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	p := newRecordPersister(ctx, store, l, Generation{}, true, nil)
	require.NoError(t, p.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
		Resources: []apitype.ResourceV3{{URN: urn, Type: tokens.Type(bucketType), Custom: true, ID: "logs-1"}},
	}}))

	marks := newLedger()
	marks.noteMarked(urn, info, nil)
	found, notes, err := sweepUnrecorded(ctx, store, testKeys, marks,
		map[Family]Lister{FamilyAWS: &fakeLister{objs: []LiveObject{{ID: "logs-1", Tags: want}}}})
	require.NoError(t, err)
	assert.Empty(t, found)
	assert.Empty(t, notes)

	found, _, err = sweepUnrecorded(ctx, store, testKeys, marks,
		map[Family]Lister{FamilyAWS: unmeasuredLister{objs: []LiveObject{{ID: "logs-9", Tags: want}}}})
	require.NoError(t, err)
	require.Len(t, found, 1, "a listing of the service's own store is swept, its consistency claimed or not")
	assert.Equal(t, resource.ID("logs-9"), found[0].ID)

	gcpURN := resource.NewURN("test", "test", "", "gcp:x/y:Y", "thing")
	gcpMarks := newLedger()
	gcpMarks.noteMarked(gcpURN, TypeInfo{Type: "gcp:x/y:Y", Family: FamilyGoogle, TFType: "google_not_a_type",
		Property: "labels", Shape: ShapeMap}, &deploy.BindRequest{URN: gcpURN, Type: "gcp:x/y:Y"})
	found, notes, err = sweepUnrecorded(ctx, store, testKeys, gcpMarks, map[Family]Lister{FamilyGoogle: newGCPLister()})
	require.NoError(t, err)
	assert.Empty(t, found, "Cloud Asset Inventory is not swept")
	assert.Empty(t, notes, "and nothing was listed")
}

type unmeasuredLister struct{ objs []LiveObject }

func (unmeasuredLister) Covers(TypeInfo) (bool, string) { return true, "" }
func (unmeasuredLister) Consistent() bool               { return false }
func (f unmeasuredLister) ListMarked(
	_ context.Context, _ TypeInfo, _ deploy.BindRequest, want map[string]string,
) ([]LiveObject, error) {
	return filterMarked(f.objs, want), nil
}
