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

// The acceptance tests of INTENTIUS/plunge#51, against the local record store and the in-process engine, with a
// deploytest provider standing in for pulumi-aws and an in-memory map standing in for the cloud. No emulator and
// no cloud: some of these tests would delete, or write markers over, another stack's object, so they run on fakes
// only.
//
//   - an import of a create-only-tags type is refused before any step and before any provider call, naming the URN
//     and the type; red first, without the vet, the import step runs and marking the object would replace it;
//   - an import whose live object carries another stack's marker is refused, and nothing is written; red first,
//     without the vet, the update after the import writes this stack's markers over the other stack's;
//   - an unmarked object of a taggable type is imported and marked in place;
//   - the delete half of a replacement goes through plunge#37's vet: a replace of an object whose live marker names
//     another stack, or that carries none, is refused before the replacement's first step, both create-before-delete
//     and delete-before-replace; red first, without the vet, the replacement deletes it; a replace of this stack's
//     own object goes ahead.
//
// Written for plunge#51 and not run in that unit.

import (
	"context"
	"sync"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/display"
	"github.com/pulumi/pulumi/pkg/v3/engine"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

const thingType = "aws:m/thing:Thing"

// liveMapping is a bridged mapping with a bucket whose tags update in place, a bucket policy with no tags argument
// (record-owned), and a thing whose tags force a new resource (create-only).
const liveMapping = `{
  "provider": {"resources": {
    "aws_s3_bucket": {"bucket": {"type": 4, "optional": true}, "tags": {"type": 6, "optional": true, "element": {"schema": {"type": 4}}}},
    "aws_s3_bucket_policy": {"bucket": {"type": 4, "optional": true}, "policy": {"type": 4, "optional": true}},
    "aws_thing": {"tags": {"type": 6, "optional": true, "forceNew": true, "element": {"schema": {"type": 4}}}}
  }},
  "resources": {
    "aws_s3_bucket": {"tok": "aws:s3/bucket:Bucket", "fields": {}},
    "aws_s3_bucket_policy": {"tok": "aws:s3/bucketPolicy:BucketPolicy", "fields": {}},
    "aws_thing": {"tok": "aws:m/thing:Thing", "fields": {}}
  }
}`

// liveCloud is the live side, by object ID: what a Read returns, and every create, update and delete. A Read of an
// ID it does not hold finds nothing.
type liveCloud struct {
	mu      sync.Mutex
	objects map[resource.ID]resource.PropertyMap
	reads   int
	next    int
	creates []resource.ID
	updates map[resource.ID][]resource.PropertyMap
	deleted []resource.ID
}

func newLiveCloud() *liveCloud {
	return &liveCloud{
		objects: map[resource.ID]resource.PropertyMap{},
		updates: map[resource.ID][]resource.PropertyMap{},
	}
}

// put places an object in the cloud, as something outside this stack made it.
func (c *liveCloud) put(id resource.ID, props map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.objects[id] = resource.NewPropertyMapFromMap(props)
}

// retag replaces an object's tags, the way another stack claiming it would.
func (c *liveCloud) retag(id resource.ID, tags map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	obj := c.objects[id].Copy()
	obj["tags"] = resource.NewProperty(resource.NewPropertyMapFromMap(toAny(tags)))
	c.objects[id] = obj
}

func (c *liveCloud) tags(id resource.ID) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return stringTags(c.objects[id]["tags"])
}

func (c *liveCloud) readCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *liveCloud) resetReads() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads = 0
}

func (c *liveCloud) createCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.creates)
}

func (c *liveCloud) updateCount(id resource.ID) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.updates[id])
}

func (c *liveCloud) wasDeleted(id resource.ID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, d := range c.deleted {
		if d == id {
			return true
		}
	}
	return false
}

func (c *liveCloud) loaders() []*deploytest.ProviderLoader {
	return []*deploytest.ProviderLoader{
		deploytest.NewProviderLoader("aws", semver.MustParse("7.0.0"), func() (plugin.Provider, error) {
			return c.provider(), nil
		}),
	}
}

func (c *liveCloud) provider() *deploytest.Provider {
	return &deploytest.Provider{
		Version: semver.MustParse("7.0.0"),
		GetMappingF: func(_ context.Context, req plugin.GetMappingRequest) (plugin.GetMappingResponse, error) {
			if req.Key != "terraform" {
				return plugin.GetMappingResponse{}, nil
			}
			return plugin.GetMappingResponse{Provider: "aws", Data: []byte(liveMapping)}, nil
		},
		// A changed bucket name replaces; changed tags replace a thing and update anything else in place.
		DiffF: func(_ context.Context, req plugin.DiffRequest) (plugin.DiffResult, error) {
			olds, news := resource.ToResourcePropertyMap(req.OldInputs), resource.ToResourcePropertyMap(req.NewInputs)
			var replace, changed []resource.PropertyKey
			for _, k := range []resource.PropertyKey{"bucket", "policy", "tags"} {
				if olds[k].DeepEquals(news[k]) {
					continue
				}
				changed = append(changed, k)
				if k == "bucket" || (k == "tags" && req.URN.Type() == thingType) {
					replace = append(replace, k)
				}
			}
			if len(changed) == 0 {
				return plugin.DiffResult{Changes: plugin.DiffNone}, nil
			}
			return plugin.DiffResult{Changes: plugin.DiffSome, ChangedKeys: changed, ReplaceKeys: replace}, nil
		},
		CreateF: func(_ context.Context, req plugin.CreateRequest) (plugin.CreateResponse, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.next++
			id := resource.ID(req.URN.Name() + "-" + string(rune('0'+c.next)))
			if !req.Preview {
				c.objects[id] = resource.ToResourcePropertyMap(req.Properties)
				c.creates = append(c.creates, id)
			}
			return plugin.CreateResponse{ID: id, Properties: req.Properties, Status: resource.StatusOK}, nil
		},
		UpdateF: func(_ context.Context, req plugin.UpdateRequest) (plugin.UpdateResponse, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if !req.Preview {
				props := resource.ToResourcePropertyMap(req.NewInputs)
				c.objects[req.ID] = props
				c.updates[req.ID] = append(c.updates[req.ID], props)
			}
			return plugin.UpdateResponse{Properties: req.NewInputs, Status: resource.StatusOK}, nil
		},
		ReadF: func(_ context.Context, req plugin.ReadRequest) (plugin.ReadResponse, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.reads++
			obj, ok := c.objects[req.ID]
			if !ok {
				return plugin.ReadResponse{ReadResult: plugin.ReadResult{ID: req.ID}, Status: resource.StatusOK}, nil
			}
			out := resource.FromResourcePropertyMap(obj.Copy())
			in := resource.FromResourcePropertyMap(obj.Copy())
			return plugin.ReadResponse{
				ReadResult: plugin.ReadResult{ID: req.ID, Outputs: &out, Inputs: &in},
				Status:     resource.StatusOK,
			}, nil
		},
		DeleteF: func(_ context.Context, req plugin.DeleteRequest) (plugin.DeleteResponse, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			delete(c.objects, req.ID)
			c.deleted = append(c.deleted, req.ID)
			return plugin.DeleteResponse{Status: resource.StatusOK}, nil
		},
	}
}

// liveRes is one registration of a liveProgram.
type liveRes struct {
	typ      tokens.Type
	name     string
	inputs   map[string]any
	importID resource.ID
	dbr      bool
}

// liveProgram registers *regs, read when the program runs.
func liveProgram(regs *[]liveRes) deploytest.LanguageRuntimeFactory {
	return deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		for _, r := range *regs {
			opts := deploytest.ResourceOptions{
				Inputs:   resource.NewPropertyMapFromMap(r.inputs),
				ImportID: r.importID,
			}
			if r.dbr {
				dbr := true
				opts.DeleteBeforeReplace = &dbr
			}
			if _, err := monitor.RegisterResource(r.typ, r.name, true, opts); err != nil {
				return err
			}
		}
		return nil
	})
}

func liveHost(cloud *liveCloud, regs *[]liveRes) deploytest.PluginHostFactory {
	return deploytest.NewPluginHostF(nil, nil, liveProgram(regs), nil, nil, cloud.loaders()...)
}

// opsFor is every step operation the run reported for the resource named name.
func opsFor(events []engine.Event, name string) []display.StepOp {
	var out []display.StepOp
	for _, m := range steps(events)[name] {
		out = append(out, m.Op)
	}
	return out
}

func thingURN(name string) resource.URN {
	return resource.NewURN("test", "test", "", thingType, name)
}

// stackTags is a resource's markers as stack's own run would write them, plus team=a.
func stackTags(t *testing.T, stack string, typ tokens.Type, name string) map[string]string {
	t.Helper()
	m, err := markers.For(resource.NewURN(tokens.QName(stack), "test", "", typ, name), markers.EncodingTags)
	require.NoError(t, err)
	m["team"] = "a"
	return m
}

// --- The import vet on its own, over a fake provider.

func vetImport(t *testing.T, cloud *liveCloud, l *ledger, urn resource.URN, id resource.ID) error {
	t.Helper()
	return newImportVetter(newCatalog(), l).VetImport(context.Background(), deploy.ImportVetRequest{
		URN: urn, Type: urn.Type(), ID: id, Provider: cloud.provider(),
		Inputs: resource.NewPropertyMapFromMap(map[string]any{"tags": map[string]any{"team": "a"}}),
	})
}

// A create-only-tags type is refused by name before any provider call: there is no live read to ask.
func TestImportVetRefusesCreateOnlyTagsBeforeAnyRead(t *testing.T) {
	t.Parallel()

	cloud := newLiveCloud()
	cloud.put("t-1", map[string]any{"tags": map[string]any{"team": "a"}})
	err := vetImport(t, cloud, newLedger(), thingURN("t"), "t-1")

	var refused *ImportRefusedError
	require.ErrorAs(t, err, &refused)
	var createOnly *CreateOnlyTagsError
	require.ErrorAs(t, err, &createOnly, "the refusal is plunge's create-only-tags refusal")
	assert.Equal(t, thingURN("t"), createOnly.URN)
	assert.Contains(t, err.Error(), string(thingURN("t")))
	assert.Contains(t, err.Error(), thingType)
	assert.Contains(t, err.Error(), "never replaces a resource to mark it")
	assert.Zero(t, cloud.readCount(), "the type table decides; no provider call")

	// The same object already carrying this resource's markers is refused too: only the bind phase may supply it.
	cloud.retag("t-1", stackTags(t, "test", thingType, "t"))
	require.ErrorAs(t, vetImport(t, cloud, newLedger(), thingURN("t"), "t-1"), &createOnly)
}

// Another stack's marker, or this stack's marker for another resource, refuses the import.
func TestImportVetRefusesAnotherOwnersMarker(t *testing.T) {
	t.Parallel()

	cloud := newLiveCloud()
	cloud.put(bucketID, map[string]any{"bucket": "logs"})
	cloud.retag(bucketID, stackTags(t, "other", bucketType, "logs"))
	err := vetImport(t, cloud, newLedger(), bucketURN(), bucketID)
	var refused *ImportRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "test/other", refused.Marker)
	assert.Contains(t, err.Error(), string(bucketURN()))
	assert.Contains(t, err.Error(), `"test/other"`)
	assert.Equal(t, 1, cloud.readCount())

	cloud.retag(bucketID, stackTags(t, "test", bucketType, "elsewhere"))
	err = vetImport(t, cloud, newLedger(), bucketURN(), bucketID)
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, err.Error(), "another resource")
}

// An unmarked object, one already carrying this resource's markers, one the bind phase bound, one the read finds
// gone, and a record-owned type all pass.
func TestImportVetPasses(t *testing.T) {
	t.Parallel()

	cloud := newLiveCloud()
	cloud.put(bucketID, map[string]any{"bucket": "logs", "tags": map[string]any{"team": "a"}})
	require.NoError(t, vetImport(t, cloud, newLedger(), bucketURN(), bucketID), "unmarked")

	cloud.retag(bucketID, stackTags(t, "test", bucketType, "logs"))
	require.NoError(t, vetImport(t, cloud, newLedger(), bucketURN(), bucketID), "already this resource's")

	require.NoError(t, vetImport(t, cloud, newLedger(), bucketURN(), "gone"), "the import step reports it")

	// Bound by the bind phase: no read, even of another stack's object (the bind found it by this URN's markers).
	cloud.retag(bucketID, stackTags(t, "other", bucketType, "logs"))
	cloud.resetReads()
	l := newLedger()
	l.record(bucketURN(), func(e *ownershipEntry) { e.Bound = bucketID })
	require.NoError(t, vetImport(t, cloud, l, bucketURN(), bucketID))
	assert.Zero(t, cloud.readCount())

	// Record-owned: no marker to read or write.
	cloud.put("pol-1", map[string]any{"bucket": "logs", "policy": "{}"})
	require.NoError(t, vetImport(t, cloud, newLedger(), policyURN(), "pol-1"))
	assert.Zero(t, cloud.readCount())
}

// --- Imports through the engine.

// Red first: without the vet, an `import:` of a create-only-tags type runs the import step; the injected markers
// then make its diff a replace, which would replace the object to mark it.
func TestWithoutTheVetCreateOnlyImportRuns(t *testing.T) {
	t.Parallel()

	cloud := newLiveCloud()
	cloud.put("t-1", map[string]any{"tags": map[string]any{"team": "a"}})
	regs := []liveRes{{typ: thingType, name: "t", inputs: map[string]any{"tags": map[string]any{"team": "a"}},
		importID: "t-1"}}
	events, _ := run(t, newCountingStore(t), liveHost(cloud, &regs), runConfig{markers: true, dryRun: true})
	ops := opsFor(events, "t")
	assert.Contains(t, ops, deploy.OpImport)
	assert.Contains(t, ops, deploy.OpReplace, "marking the imported object replaces it")
}

// With the vet, the run is refused before any step for the resource, naming its URN and type; nothing is imported,
// recorded, created or deleted.
func TestCreateOnlyImportIsRefusedBeforeAnyStep(t *testing.T) {
	t.Parallel()

	for _, dryRun := range []bool{true, false} {
		cloud := newLiveCloud()
		cloud.put("t-1", map[string]any{"tags": map[string]any{"team": "a"}})
		store := newCountingStore(t)
		regs := []liveRes{{typ: thingType, name: "t", inputs: map[string]any{"tags": map[string]any{"team": "a"}},
			importID: "t-1"}}
		events, err := run(t, store, liveHost(cloud, &regs),
			runConfig{markers: true, vet: true, persist: true, dryRun: dryRun})
		require.Error(t, err, "dryRun=%v", dryRun)
		msg := errorText(events)
		assert.Contains(t, msg, string(thingURN("t")))
		assert.Contains(t, msg, thingType)
		assert.Empty(t, opsFor(events, "t"), "dryRun=%v: no step for the resource", dryRun)
		assert.Zero(t, cloud.readCount(), "dryRun=%v: refused before the import's read", dryRun)
		assert.Zero(t, cloud.createCount())
		assert.False(t, cloud.wasDeleted("t-1"))
		assert.False(t, hasRecord(t, store, thingURN("t")))
	}
}

// Red first: without the vet, an `import:` of an object another stack's marker names succeeds, and the update that
// follows writes this stack's markers over the other stack's.
func TestWithoutTheVetImportOverwritesAnotherStacksMarker(t *testing.T) {
	t.Parallel()

	cloud := newLiveCloud()
	cloud.put(bucketID, map[string]any{"bucket": "logs"})
	cloud.retag(bucketID, stackTags(t, "other", bucketType, "logs"))
	regs := []liveRes{{typ: bucketType, name: "logs", importID: bucketID,
		inputs: map[string]any{"bucket": "logs", "tags": map[string]any{"team": "a"}}}}
	_, err := run(t, newCountingStore(t), liveHost(cloud, &regs), runConfig{markers: true, persist: true})
	require.NoError(t, err)
	stack, _, ok := markers.Owner(cloud.tags(bucketID))
	require.True(t, ok)
	assert.Equal(t, "test/test", stack, "stack other's marker was overwritten")
}

// With the vet, that import refuses the run: the refusal names the URN and the stack the marker names, and the
// object keeps the other stack's marker.
func TestImportOfAnotherStacksObjectIsRefused(t *testing.T) {
	t.Parallel()

	cloud := newLiveCloud()
	cloud.put(bucketID, map[string]any{"bucket": "logs"})
	cloud.retag(bucketID, stackTags(t, "other", bucketType, "logs"))
	store := newCountingStore(t)
	regs := []liveRes{{typ: bucketType, name: "logs", importID: bucketID,
		inputs: map[string]any{"bucket": "logs", "tags": map[string]any{"team": "a"}}}}
	events, err := run(t, store, liveHost(cloud, &regs), runConfig{markers: true, vet: true, persist: true})
	require.Error(t, err)
	msg := errorText(events)
	assert.Contains(t, msg, string(bucketURN()))
	assert.Contains(t, msg, `"test/other"`)
	assert.NotContains(t, opsFor(events, "logs"), deploy.OpImport)
	assert.Zero(t, cloud.updateCount(bucketID))
	stack, _, _ := markers.Owner(cloud.tags(bucketID))
	assert.Equal(t, "test/other", stack)
	assert.False(t, hasRecord(t, store, bucketURN()))
}

// An unmarked object of a taggable type is imported and marked in place: one update, nothing replaced.
func TestImportOfUnmarkedObjectIsMarkedInPlace(t *testing.T) {
	t.Parallel()

	cloud := newLiveCloud()
	cloud.put(bucketID, map[string]any{"bucket": "logs", "tags": map[string]any{"team": "a"}})
	store := newCountingStore(t)
	regs := []liveRes{{typ: bucketType, name: "logs", importID: bucketID,
		inputs: map[string]any{"bucket": "logs", "tags": map[string]any{"team": "a"}}}}
	events, err := run(t, store, liveHost(cloud, &regs), runConfig{markers: true, vet: true, persist: true})
	require.NoError(t, err)
	ops := opsFor(events, "logs")
	assert.Contains(t, ops, deploy.OpImport)
	assert.NotContains(t, ops, deploy.OpReplace)
	assert.Equal(t, 1, cloud.updateCount(bucketID))
	assert.True(t, markers.Carries(cloud.tags(bucketID), stackTags(t, "test", bucketType, "logs")))
	assert.Zero(t, cloud.createCount())
	assert.True(t, hasRecord(t, store, bucketURN()))
}

// --- The delete half of a replacement.

// replaceSetup creates the bucket with this stack's markers, then returns the program renaming it, which the
// provider answers with a replace.
func replaceSetup(t *testing.T, cloud *liveCloud, dbr bool) (*countingStore, *[]liveRes, resource.ID) {
	t.Helper()
	store := newCountingStore(t)
	regs := []liveRes{{typ: bucketType, name: "logs",
		inputs: map[string]any{"bucket": "logs", "tags": map[string]any{"team": "a"}}}}
	_, err := run(t, store, liveHost(cloud, &regs), runConfig{markers: true, vet: true, persist: true})
	require.NoError(t, err)
	require.Equal(t, 1, cloud.createCount())
	original := cloud.creates[0]
	cloud.resetReads()
	regs = []liveRes{{typ: bucketType, name: "logs", dbr: dbr,
		inputs: map[string]any{"bucket": "logs-2", "tags": map[string]any{"team": "a"}}}}
	return store, &regs, original
}

// Red first: without the vet, a replace deletes an object whose live marker names another stack.
func TestWithoutTheVetAReplaceDeletesAnotherStacksObject(t *testing.T) {
	t.Parallel()

	for _, dbr := range []bool{false, true} {
		cloud := newLiveCloud()
		store, regs, original := replaceSetup(t, cloud, dbr)
		cloud.retag(original, stackTags(t, "other", bucketType, "logs"))
		_, err := run(t, store, liveHost(cloud, regs), runConfig{markers: true, persist: true, refresh: true})
		require.NoError(t, err)
		assert.True(t, cloud.wasDeleted(original), "dbr=%v: the replacement deleted stack other's bucket", dbr)
	}
}

// With the vet, the replace is refused before its first step, create-before-delete and delete-before-replace alike,
// for another stack's marker and for a missing one; nothing is created or deleted and the record survives.
func TestReplaceOfAnObjectThisStackDoesNotMarkIsRefused(t *testing.T) {
	t.Parallel()

	for _, dbr := range []bool{false, true} {
		for _, foreign := range []bool{true, false} {
			cloud := newLiveCloud()
			store, regs, original := replaceSetup(t, cloud, dbr)
			if foreign {
				cloud.retag(original, stackTags(t, "other", bucketType, "logs"))
			} else {
				cloud.retag(original, map[string]string{"team": "a"})
			}
			events, err := run(t, store, liveHost(cloud, regs),
				runConfig{markers: true, vet: true, persist: true, refresh: true})
			require.Error(t, err, "dbr=%v foreign=%v", dbr, foreign)
			msg := errorText(events)
			assert.Contains(t, msg, string(bucketURN()))
			assert.Contains(t, msg, "finish a replacement")
			if foreign {
				assert.Contains(t, msg, `"test/other"`)
			} else {
				assert.Contains(t, msg, "carries no "+markers.TagStack+" marker")
			}
			assert.False(t, cloud.wasDeleted(original), "dbr=%v foreign=%v", dbr, foreign)
			assert.Equal(t, 1, cloud.createCount(), "dbr=%v foreign=%v: no replacement was created", dbr, foreign)
			assert.True(t, hasRecord(t, store, bucketURN()))
		}
	}
}

// A replace of this stack's own object goes ahead. With refresh on the vet reads the refreshed outputs, so the only
// read is the refresh's, and the original is asked about once (when the replace is decided), not again among the
// run's deletes.
func TestReplaceOfThisStacksObjectGoesAhead(t *testing.T) {
	t.Parallel()

	for _, dbr := range []bool{false, true} {
		cloud := newLiveCloud()
		store, regs, original := replaceSetup(t, cloud, dbr)
		events, err := run(t, store, liveHost(cloud, regs),
			runConfig{markers: true, vet: true, persist: true, refresh: true})
		require.NoError(t, err, "dbr=%v", dbr)
		assert.True(t, cloud.wasDeleted(original), "dbr=%v", dbr)
		assert.Equal(t, 2, cloud.createCount(), "dbr=%v", dbr)
		assert.Equal(t, 1, cloud.readCount(), "dbr=%v: the refresh's read and no other", dbr)
		assert.Contains(t, opsFor(events, "logs"), deploy.OpReplace)
	}
}
