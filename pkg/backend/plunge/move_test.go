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

// INTENTIUS/plunge#56: `plunge stack move` rewrites the live markers through the provider's Update and moves the
// records, never creating or deleting a cloud object. Every test runs on a local record store in a temp dir and a
// fake provider; nothing reaches a cloud.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
)

var (
	moveSrc  = moveSide{ref: "proj/src", project: "proj", stack: "src", keys: newStackKeys("proj", "src")}
	moveDest = moveSide{ref: "proj/dst", project: "proj", stack: "dst", keys: newStackKeys("proj", "dst")}
)

const (
	moveSrcRoot  = resource.URN("urn:pulumi:src::proj::pulumi:pulumi:Stack::proj-src")
	moveDestRoot = resource.URN("urn:pulumi:dst::proj::pulumi:pulumi:Stack::proj-dst")
	moveProvURN  = resource.URN("urn:pulumi:src::proj::pulumi:providers:pkgA::default")
	moveProvID   = resource.ID("prov-1")
	moveResURN   = resource.URN("urn:pulumi:src::proj::pkgA:m:typA::bucket")
	moveResDest  = resource.URN("urn:pulumi:dst::proj::pkgA:m:typA::bucket")
	moveStayURN  = resource.URN("urn:pulumi:src::proj::pkgA:m:typA::stays")
	moveResID    = resource.ID("bucket-1")
)

func putMoveRecord(t *testing.T, store staterecord.Store, keys stackKeys, rec resourceRecord) {
	t.Helper()
	rec.Format = resourceFormat
	payload, err := json.Marshal(rec)
	require.NoError(t, err)
	_, err = store.PutIfAbsent(context.Background(), keys.resource(rec.URN), payload)
	require.NoError(t, err)
}

func tagsFor(t *testing.T, urn resource.URN) map[string]any {
	t.Helper()
	want, err := markers.For(urn, markers.EncodingTags)
	require.NoError(t, err)
	tags := map[string]any{"env": "dev"}
	for k, v := range want {
		tags[k] = v
	}
	return tags
}

// seedMove writes a source stack with a root, a provider, a marked bucket and an unrelated resource that stays,
// and a destination stack with only its root.
func seedMove(t *testing.T, store staterecord.Store) {
	t.Helper()
	putMoveRecord(t, store, moveSrc.keys, resourceRecord{URN: moveSrcRoot, States: []apitype.ResourceV3{{
		URN: moveSrcRoot, Type: resource.RootStackType,
	}}})
	putMoveRecord(t, store, moveDest.keys, resourceRecord{URN: moveDestRoot, States: []apitype.ResourceV3{{
		URN: moveDestRoot, Type: resource.RootStackType,
	}}})
	putMoveRecord(t, store, moveSrc.keys, resourceRecord{URN: moveProvURN, States: []apitype.ResourceV3{{
		URN: moveProvURN, Custom: true, ID: moveProvID, Type: "pulumi:providers:pkgA",
		Inputs: map[string]any{"version": "1.0.0"}, Outputs: map[string]any{"version": "1.0.0"},
	}}})
	tags := tagsFor(t, moveResURN)
	putMoveRecord(t, store, moveSrc.keys, resourceRecord{
		URN: moveResURN,
		States: []apitype.ResourceV3{{
			URN: moveResURN, Custom: true, ID: moveResID, Type: "pkgA:m:typA", Parent: moveSrcRoot,
			Provider: string(moveProvURN) + "::" + string(moveProvID),
			Inputs:   map[string]any{"name": "logs", "tags": tags},
			Outputs:  map[string]any{"name": "logs", "tags": tags, "tagsAll": tags},
		}},
		Ownership: &ownershipEntry{By: OwnershipMarker, State: MarkerInjected, Marker: stringMap(tags)},
	})
	putMoveRecord(t, store, moveSrc.keys, resourceRecord{URN: moveStayURN, States: []apitype.ResourceV3{{
		URN: moveStayURN, Custom: true, ID: "stays-1", Type: "pkgA:m:typA", Parent: moveSrcRoot,
		Provider: string(moveProvURN) + "::" + string(moveProvID),
	}}})
}

func stringMap(m map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if markers.IsMarkerKey(k) {
			out[k] = v.(string)
		}
	}
	return out
}

// moveFake is a provider that answers Read with the live tags it holds and applies an Update to them. Create and
// Delete fail the test: a move never calls either.
type moveFake struct {
	t       *testing.T
	mu      sync.Mutex
	live    map[string]any
	replace bool
	updates []plugin.UpdateRequest
}

func (f *moveFake) plugins() *plugin.Context {
	t := f.t
	loader := deploytest.NewProviderLoader("pkgA", semver.MustParse("1.0.0"), func() (plugin.Provider, error) {
		return &deploytest.Provider{
			ReadF: func(_ context.Context, req plugin.ReadRequest) (plugin.ReadResponse, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				out := resource.FromResourcePropertyMap(resource.NewPropertyMapFromMap(map[string]any{
					"name": "logs", "tags": f.live, "tagsAll": f.live,
				}))
				return plugin.ReadResponse{ReadResult: plugin.ReadResult{ID: req.ID, Outputs: &out}}, nil
			},
			CheckF: func(_ context.Context, req plugin.CheckRequest) (plugin.CheckResponse, error) {
				return plugin.CheckResponse{Properties: req.NewInputs}, nil
			},
			DiffF: func(_ context.Context, req plugin.DiffRequest) (plugin.DiffResult, error) {
				d := plugin.DiffResult{Changes: plugin.DiffSome, ChangedKeys: []resource.PropertyKey{"tags"}}
				if f.replace {
					d.ReplaceKeys = []resource.PropertyKey{"tags"}
				}
				return d, nil
			},
			UpdateF: func(_ context.Context, req plugin.UpdateRequest) (plugin.UpdateResponse, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.updates = append(f.updates, req)
				news := resource.ToResourcePropertyMap(req.NewInputs)
				f.live = news["tags"].Mappable().(map[string]any)
				return plugin.UpdateResponse{Properties: req.NewInputs, Status: resource.StatusOK}, nil
			},
			CreateF: func(context.Context, plugin.CreateRequest) (plugin.CreateResponse, error) {
				t.Error("a move called Create")
				return plugin.CreateResponse{}, errors.New("no create in a move")
			},
			DeleteF: func(context.Context, plugin.DeleteRequest) (plugin.DeleteResponse, error) {
				t.Error("a move called Delete")
				return plugin.DeleteResponse{}, errors.New("no delete in a move")
			},
		}, nil
	})
	host := deploytest.NewPluginHost(nil, nil, nil, loader)
	t.Cleanup(func() { contract.IgnoreClose(host) })
	pctx, err := plugin.NewContextWithHost(context.Background(), nil, nil, host, "", "", nil)
	require.NoError(t, err)
	t.Cleanup(func() { contract.IgnoreClose(pctx) })
	return pctx
}

func newMoveFake(t *testing.T) *moveFake {
	return &moveFake{t: t, live: tagsFor(t, moveResURN)}
}

func moveOpts(pctx *plugin.Context, confirmed bool, urns ...resource.URN) MoveOptions {
	return MoveOptions{URNs: urns, Plugins: pctx, Confirmed: confirmed}
}

func allKeys(t *testing.T, store staterecord.Store) []string {
	t.Helper()
	keys, err := store.List(context.Background(), "")
	require.NoError(t, err)
	return keys
}

func TestMoveRewritesMarkersAndRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	seedMove(t, store)
	fake := newMoveFake(t)
	pctx := fake.plugins()
	before := allKeys(t, store)

	// Unconfirmed: the plan, and nothing written.
	plan, err := newMover(store, moveSrc, moveDest, moveOpts(pctx, false, moveResURN)).run(ctx)
	require.NoError(t, err)
	assert.False(t, plan.Written)
	require.Len(t, plan.Resources, 1)
	assert.Equal(t, MovePlanned, plan.Resources[0].Status)
	assert.Equal(t, moveResDest, plan.Resources[0].DestinationURN)
	assert.True(t, plan.Resources[0].MarkerWrite)
	assert.Equal(t, before, allKeys(t, store))
	assert.Empty(t, fake.updates)

	res, err := newMover(store, moveSrc, moveDest, moveOpts(pctx, true, moveResURN)).run(ctx)
	require.NoError(t, err)
	assert.True(t, res.Written)
	assert.Equal(t, MoveMoved, res.Resources[0].Status)

	// One tags-only Update, whose new tags name the destination.
	require.Len(t, fake.updates, 1)
	wantTags := tagsFor(t, moveResDest)
	assert.Equal(t, wantTags, fake.live)

	// The source no longer holds the bucket; the destination does, with its own URN and markers, and a copy of the
	// provider under plunge's derived ID.
	srcL, err := load(ctx, store, moveSrc.keys)
	require.NoError(t, err)
	srcRecs, _ := recordsByURN(srcL)
	assert.NotContains(t, srcRecs, moveResURN)
	assert.Contains(t, srcRecs, moveStayURN)

	destL, err := load(ctx, store, moveDest.keys)
	require.NoError(t, err)
	destRecs, _ := recordsByURN(destL)
	moved := destRecs[moveResDest]
	require.NotNil(t, moved)
	live, ok := liveState(moved)
	require.True(t, ok)
	assert.Equal(t, moveResID, live.ID)
	assert.Equal(t, moveDestRoot, live.Parent)
	assert.Equal(t, wantTags, live.Inputs["tags"])
	assert.Equal(t, stringMap(wantTags), moved.Ownership.Marker)

	destProv := moveDest.rehome(moveProvURN)
	require.Contains(t, destRecs, destProv)
	provLive, _ := liveState(destRecs[destProv])
	assert.Equal(t, string(destProv)+"::"+string(provLive.ID), live.Provider)

	// The markers name the destination, so deletevet's check (markers.Carries against the URN's markers) passes.
	want, err := markers.For(moveResDest, markers.EncodingTags)
	require.NoError(t, err)
	assert.True(t, markers.Carries(stringMap(fake.live), want))

	// Running it again finds it already moved and writes nothing.
	again, err := newMover(store, moveSrc, moveDest, moveOpts(pctx, true, moveResURN)).run(ctx)
	require.NoError(t, err)
	assert.Equal(t, MoveAlreadyMoved, again.Resources[0].Status)
	assert.Len(t, fake.updates, 1)
}

// failDeletes refuses every delete, as a move that stops after writing the destination's records.
type failDeletes struct{ staterecord.Store }

func (failDeletes) Delete(context.Context, string, string) error { return errors.New("interrupted") }

func TestMoveInterruptedIsFinishedByARerun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	seedMove(t, store)
	fake := newMoveFake(t)
	pctx := fake.plugins()

	_, err = newMover(failDeletes{store}, moveSrc, moveDest, moveOpts(pctx, true, moveResURN)).run(ctx)
	var interrupted *MoveInterruptedError
	require.ErrorAs(t, err, &interrupted)
	require.Len(t, fake.updates, 1)

	res, err := newMover(store, moveSrc, moveDest, moveOpts(pctx, true, moveResURN)).run(ctx)
	require.NoError(t, err)
	assert.Equal(t, MoveResumed, res.Resources[0].Status)
	assert.Len(t, fake.updates, 1, "the re-run only removes the source record")

	srcL, err := load(ctx, store, moveSrc.keys)
	require.NoError(t, err)
	srcRecs, _ := recordsByURN(srcL)
	assert.NotContains(t, srcRecs, moveResURN)
}

func TestMoveRefusesWhenTheDestinationOwnsTheURN(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	seedMove(t, store)
	putMoveRecord(t, store, moveDest.keys, resourceRecord{URN: moveResDest, States: []apitype.ResourceV3{{
		URN: moveResDest, Custom: true, ID: "another-bucket", Type: "pkgA:m:typA", Parent: moveDestRoot,
	}}})
	fake := newMoveFake(t)
	before := allKeys(t, store)

	_, err = newMover(store, moveSrc, moveDest, moveOpts(fake.plugins(), true, moveResURN)).run(ctx)
	var refused *MoveRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Error(), "the destination already owns")
	assert.Equal(t, before, allKeys(t, store))
	assert.Empty(t, fake.updates)
}

func TestMoveRefusesAReplacingMarkerRewrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	seedMove(t, store)
	fake := newMoveFake(t)
	fake.replace = true
	before := allKeys(t, store)

	_, err = newMover(store, moveSrc, moveDest, moveOpts(fake.plugins(), true, moveResURN)).run(ctx)
	var refused *MoveRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Error(), "would replace the live object")
	assert.Equal(t, before, allKeys(t, store))
	assert.Empty(t, fake.updates)
}

func TestMoveRefusesAThirdStacksMarker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	seedMove(t, store)
	fake := newMoveFake(t)
	fake.live = tagsFor(t, "urn:pulumi:other::proj::pkgA:m:typA::bucket")

	_, err = newMover(store, moveSrc, moveDest, moveOpts(fake.plugins(), true, moveResURN)).run(ctx)
	var refused *MoveRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Error(), "names neither")
	assert.Empty(t, fake.updates)
}

func TestMoveRefusesLeavingADependentBehind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	seedMove(t, store)
	child := resource.URN("urn:pulumi:src::proj::pkgA:m:typA::child")
	putMoveRecord(t, store, moveSrc.keys, resourceRecord{URN: child, States: []apitype.ResourceV3{{
		URN: child, Custom: true, ID: "child-1", Type: "pkgA:m:typA", Parent: moveSrcRoot,
		Dependencies: []resource.URN{moveResURN},
		Provider:     string(moveProvURN) + "::" + string(moveProvID),
	}}})
	fake := newMoveFake(t)

	_, err = newMover(store, moveSrc, moveDest, moveOpts(fake.plugins(), true, moveResURN)).run(ctx)
	var refused *MoveRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Error(), string(child)+" depends on it")
	assert.Empty(t, fake.updates)
}

func TestRehomeMarkers(t *testing.T) {
	t.Parallel()
	src := tagsFor(t, moveResURN)
	labels, err := markers.For(moveResURN, markers.EncodingLabels)
	require.NoError(t, err)
	props := resource.NewPropertyMapFromMap(map[string]any{
		"tags": src,
		"metadata": map[string]any{"labels": map[string]any{
			"app": "x", markers.TagStack: labels[markers.TagStack], markers.TagURN: labels[markers.TagURN],
		}},
		"tagList": []any{
			map[string]any{"key": "env", "value": "dev"},
			map[string]any{"key": markers.TagStack, "value": "proj/src"},
			map[string]any{"key": markers.TagURN, "value": "pkgA:m:typA::bucket"},
		},
	})

	out, written, marked, err := rehomeMarkers(props, moveResDest)
	require.NoError(t, err)
	require.True(t, marked)
	wantTags, err := markers.For(moveResDest, markers.EncodingTags)
	require.NoError(t, err)
	wantLabels, err := markers.For(moveResDest, markers.EncodingLabels)
	require.NoError(t, err)
	assert.Equal(t, wantTags, written)

	m := out.Mappable()
	assert.Equal(t, tagsFor(t, moveResDest), m["tags"])
	assert.Equal(t, map[string]any{
		"app": "x", markers.TagStack: wantLabels[markers.TagStack], markers.TagURN: wantLabels[markers.TagURN],
	}, m["metadata"].(map[string]any)["labels"])
	assert.Equal(t, []any{
		map[string]any{"key": "env", "value": "dev"},
		map[string]any{"key": markers.TagStack, "value": "proj/dst"},
		map[string]any{"key": markers.TagURN, "value": "pkgA:m:typA::bucket"},
	}, m["tagList"])

	_, _, marked, err = rehomeMarkers(resource.NewPropertyMapFromMap(map[string]any{"name": "x"}), moveResDest)
	require.NoError(t, err)
	assert.False(t, marked)
}
