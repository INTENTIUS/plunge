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

// The acceptance tests of INTENTIUS/plunge#6, against the local record store and the in-process engine:
//
//   - two concurrent updates on disjoint resources of one stack both land, and VerifyIntegrity passes;
//   - a run from an older program version does not delete what a newer run created;
//   - a missing parent is refused by name;
//   - an unchanged record is not rewritten.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/engine"
	lt "github.com/pulumi/pulumi/pkg/v3/engine/lifecycletest/framework"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/pkg/v3/secrets/b64"
	"github.com/pulumi/pulumi/pkg/v3/util/cancel"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/sig"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
)

// countingStore counts the conditional writes and deletes that reach the store.
type countingStore struct {
	staterecord.Store
	mu     sync.Mutex
	writes map[string]int
}

func newCountingStore(t *testing.T) *countingStore {
	local, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	return &countingStore{Store: local, writes: map[string]int{}}
}

func (c *countingStore) count(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes[key]++
}

func (c *countingStore) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.writes {
		n += v
	}
	return n
}

func (c *countingStore) PutIfVersion(ctx context.Context, key string, payload []byte, expected string) (string, error) {
	c.count(key)
	return c.Store.PutIfVersion(ctx, key, payload, expected)
}

func (c *countingStore) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	c.count(key)
	return c.Store.PutIfAbsent(ctx, key, payload)
}

func (c *countingStore) Delete(ctx context.Context, key string, expected string) error {
	c.count(key)
	return c.Store.Delete(ctx, key, expected)
}

var testKeys = newStackKeys("test", "test")

// runUpdate is the plunge backend's apply without the display: assemble the snapshot from the records, run an
// update with the record persister, the deterministic provider IDs and the delete guard for run.
func runUpdate(t *testing.T, store staterecord.Store, hostF deploytest.PluginHostFactory, run Generation) error {
	t.Helper()
	ctx := context.Background()

	l, err := load(ctx, store, testKeys)
	if err != nil {
		return err
	}
	var snap *deploy.Snapshot
	if len(l.records) > 0 {
		snap, err = stack.DeserializeDeploymentV3(ctx, *l.deployment.Deployment, stack.Base64SecretsProvider{})
		if err != nil {
			return err
		}
		if err := snap.VerifyIntegrity(); err != nil {
			return err
		}
	}

	sm := b64.NewBase64SecretsManager()
	persister := newRecordPersister(ctx, store, l, run, true, sm.Decrypter())
	manager := backend.NewSnapshotManager(persister, sm, snap, nil)

	p := &lt.TestPlan{}
	project := p.GetProject()
	target := p.GetTarget(t, snap)
	target.ProviderIDs = DeterministicProviderID
	target.DeleteGuard = deleteGuard(run, l.generations)
	target.Config = config.Map{}

	host := hostF()
	defer contract.IgnoreClose(host)

	events := make(chan engine.Event)
	drained := make(chan struct{})
	go func() {
		for range events {
		}
		close(drained)
	}()

	cancelCtx, cancelSrc := cancel.NewContext(ctx)
	defer cancelSrc.Terminate()
	engineCtx := &engine.Context{
		Cancel:          cancelCtx,
		Events:          events,
		SnapshotManager: manager,
		PluginManager:   lt.NopPluginManager{},
	}
	opts := engine.UpdateOptions{
		Parallel: 4,
		HostFactory: func(context.Context, diag.Sink, diag.Sink, plugin.DebugContext) (plugin.Host, error) {
			return host, nil
		},
	}
	_, _, updateErr := engine.Update(lt.NewUpdateInfo(project, target), engineCtx, opts, false)
	closeErr := manager.Close()
	close(events)
	<-drained
	return errors.Join(updateErr, closeErr)
}

// assembled loads the records and returns the snapshot they make, which must pass VerifyIntegrity.
func assembled(t *testing.T, store staterecord.Store) *deploy.Snapshot {
	t.Helper()
	ctx := context.Background()
	l, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	snap, err := stack.DeserializeDeploymentV3(ctx, *l.deployment.Deployment, stack.Base64SecretsProvider{})
	require.NoError(t, err)
	require.NoError(t, snap.VerifyIntegrity())
	return snap
}

func byName(snap *deploy.Snapshot) map[string]resource.PropertyMap {
	out := map[string]resource.PropertyMap{}
	for _, res := range snap.Resources {
		if !res.Delete {
			out[res.URN.Name()] = res.Inputs
		}
	}
	return out
}

// pkgA's provider: creates get a counter ID, updates are in place, deletes are recorded.
func testLoaders(deleted *sync.Map) []*deploytest.ProviderLoader {
	var mu sync.Mutex
	next := 0
	return []*deploytest.ProviderLoader{
		deploytest.NewProviderLoader("pkgA", semver.MustParse("1.0.0"), func() (plugin.Provider, error) {
			return &deploytest.Provider{
				CreateF: func(_ context.Context, req plugin.CreateRequest) (plugin.CreateResponse, error) {
					mu.Lock()
					defer mu.Unlock()
					next++
					return plugin.CreateResponse{
						ID:         resource.ID(req.URN.Name() + "-" + string(rune('0'+next))),
						Properties: req.Properties,
						Status:     resource.StatusOK,
					}, nil
				},
				UpdateF: func(_ context.Context, req plugin.UpdateRequest) (plugin.UpdateResponse, error) {
					return plugin.UpdateResponse{Properties: req.NewInputs, Status: resource.StatusOK}, nil
				},
				DeleteF: func(_ context.Context, req plugin.DeleteRequest) (plugin.DeleteResponse, error) {
					if deleted != nil {
						deleted.Store(req.URN.Name(), true)
					}
					return plugin.DeleteResponse{}, nil
				},
			}, nil
		}),
	}
}

// program registers resX and resY with the given "v" inputs, and resZ when withZ is set.
func program(x, y string, withZ bool) deploytest.LanguageRuntimeFactory {
	return deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		_, err := monitor.RegisterResource("pkgA:m:typA", "resX", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{"v": x}),
		})
		if err != nil {
			return err
		}
		_, err = monitor.RegisterResource("pkgA:m:typA", "resY", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{"v": y}),
		})
		if err != nil {
			return err
		}
		if withZ {
			_, err = monitor.RegisterResource("pkgA:m:typA", "resZ", true)
		}
		return err
	})
}

func TestTwoConcurrentUpdatesOnDisjointResourcesBothLand(t *testing.T) {
	t.Parallel()

	store := newCountingStore(t)
	gen := Generation{Commit: "c1", Time: 100}

	// Base: resX and resY at v1.
	require.NoError(t, runUpdate(t, store,
		deploytest.NewPluginHostF(nil, nil, program("1", "1", false), nil, nil, testLoaders(nil)...), gen))

	// Two runs from the same base, at the same time: one changes resX only, the other resY only.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, prog := range []deploytest.LanguageRuntimeFactory{program("2", "1", false), program("1", "2", false)} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = runUpdate(t, store, deploytest.NewPluginHostF(nil, nil, prog, nil, nil, testLoaders(nil)...), gen)
		}()
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])

	// Both changes are in the records, and the assembled snapshot passes VerifyIntegrity.
	got := byName(assembled(t, store))
	assert.Equal(t, "2", got["resX"]["v"].StringValue())
	assert.Equal(t, "2", got["resY"]["v"].StringValue())
}

// Two runs that each create the stack from nothing create the root Stack and the default provider. The provider's
// ID is derived, so both arrive at the same record and the second adopts the first's.
func TestTwoFirstRunsConvergeOnOneDefaultProvider(t *testing.T) {
	t.Parallel()

	store := newCountingStore(t)
	gen := Generation{Commit: "c1", Time: 100}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = runUpdate(t, store,
				deploytest.NewPluginHostF(nil, nil, program("1", "1", false), nil, nil, testLoaders(nil)...), gen)
		}()
	}
	wg.Wait()

	// The resources themselves are created by both runs, so one run may meet a named conflict on resX's or
	// resY's record (that is claim C1's collision, outside this unit). Neither may fail on the root Stack or the
	// default provider.
	for _, err := range errs {
		if err == nil {
			continue
		}
		assert.Contains(t, err.Error(), "changed under this run")
		assert.NotContains(t, err.Error(), "pulumi:pulumi:Stack")
		assert.NotContains(t, err.Error(), "pulumi:providers:pkgA")
	}

	snap := assembled(t, store)
	var provs []resource.ID
	for _, res := range snap.Resources {
		if providers.IsProviderType(res.Type) {
			provs = append(provs, res.ID)
		}
	}
	require.Len(t, provs, 1)
}

func TestOlderRunDoesNotDeleteNewerRunsResource(t *testing.T) {
	t.Parallel()

	store := newCountingStore(t)
	var deleted sync.Map

	// The newer program (commit time 200) creates resZ alongside resX and resY.
	newer := Generation{Commit: "new", Time: 200}
	require.NoError(t, runUpdate(t, store,
		deploytest.NewPluginHostF(nil, nil, program("1", "1", true), nil, nil, testLoaders(&deleted)...), newer))

	// A run of the older program (commit time 100), which has never heard of resZ, runs next.
	older := Generation{Commit: "old", Time: 100}
	require.NoError(t, runUpdate(t, store,
		deploytest.NewPluginHostF(nil, nil, program("1", "1", false), nil, nil, testLoaders(&deleted)...), older))

	_, wasDeleted := deleted.Load("resZ")
	assert.False(t, wasDeleted, "the older run must not delete resZ")
	got := byName(assembled(t, store))
	assert.Contains(t, got, "resZ")

	// The newer program, run again without resZ, does delete it: the guard is about generations, not a pin.
	require.NoError(t, runUpdate(t, store,
		deploytest.NewPluginHostF(nil, nil, program("1", "1", false), nil, nil, testLoaders(&deleted)...), newer))
	_, wasDeleted = deleted.Load("resZ")
	assert.True(t, wasDeleted)
	assert.NotContains(t, byName(assembled(t, store)), "resZ")
}

func TestUnchangedRecordIsNotRewritten(t *testing.T) {
	t.Parallel()

	store := newCountingStore(t)
	gen := Generation{Commit: "c1", Time: 100}
	hostF := deploytest.NewPluginHostF(nil, nil, program("1", "1", false), nil, nil, testLoaders(nil)...)

	require.NoError(t, runUpdate(t, store, hostF, gen))
	before := store.total()
	require.Positive(t, before)

	// The same program again changes nothing, so no record is written.
	require.NoError(t, runUpdate(t, store, hostF, gen))
	assert.Equal(t, before, store.total(), "an unchanged run wrote %v", store.writes)

	// Changing resX writes resX's record and nothing else.
	store.mu.Lock()
	counts := map[string]int{}
	for k, v := range store.writes {
		counts[k] = v
	}
	store.mu.Unlock()
	require.NoError(t, runUpdate(t, store,
		deploytest.NewPluginHostF(nil, nil, program("2", "1", false), nil, nil, testLoaders(nil)...), gen))
	store.mu.Lock()
	defer store.mu.Unlock()
	var changed []string
	for k, v := range store.writes {
		if v != counts[k] {
			changed = append(changed, k)
		}
	}
	urnX := resource.URN("urn:pulumi:test::test::pkgA:m:typA::resX")
	assert.Equal(t, []string{testKeys.resource(urnX)}, changed)
}

// A secret re-encrypted to a new ciphertext is the same value, so its record is not rewritten.
func TestReencryptedSecretIsNotARewrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newCountingStore(t)
	urn := resource.URN("urn:pulumi:test::test::pkgA:m:typA::resS")
	dec := mapDecrypter{"ct-1": "hunter2", "ct-2": "hunter2"}

	deployment := func(ciphertext string) apitype.TypedDeployment {
		return apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{Resources: []apitype.ResourceV3{{
			URN: urn, Type: "pkgA:m:typA", Custom: true, ID: "s-1",
			Outputs: map[string]any{"password": map[string]any{sig.Key: sig.Secret, "ciphertext": ciphertext}},
		}}}}
	}

	l, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	p := newRecordPersister(ctx, store, l, Generation{}, true, dec)
	require.NoError(t, p.Save(deployment("ct-1")))
	require.Equal(t, 1, store.total())

	l, err = load(ctx, store, testKeys)
	require.NoError(t, err)
	p = newRecordPersister(ctx, store, l, Generation{}, true, dec)
	require.NoError(t, p.Save(deployment("ct-2")))
	assert.Equal(t, 1, store.total())
}

type mapDecrypter map[string]string

func (m mapDecrypter) DecryptValue(_ context.Context, ciphertext string) (string, error) {
	if v, ok := m[ciphertext]; ok {
		return v, nil
	}
	return "", errors.New("unknown ciphertext")
}

func (m mapDecrypter) BatchDecrypt(ctx context.Context, ciphertexts []string) ([]string, error) {
	out := make([]string, len(ciphertexts))
	for i, c := range ciphertexts {
		v, err := m.DecryptValue(ctx, c)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func TestMissingParentIsRefusedByName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newCountingStore(t)
	parent := resource.URN("urn:pulumi:test::test::pkgA:m:typA::parent")
	child := resource.URN("urn:pulumi:test::test::pkgA:m:typA$pkgA:m:typA::child")

	// A run writes the child's record; the parent has no record (another run deleted it, or it was never written).
	l, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	p := newRecordPersister(ctx, store, l, Generation{}, true, nil)
	require.NoError(t, p.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{Resources: []apitype.ResourceV3{{
		URN: child, Type: "pkgA:m:typA", Parent: parent,
	}}}}))

	_, err = load(ctx, store, testKeys)
	var missing *MissingReferenceError
	require.ErrorAs(t, err, &missing)
	require.Len(t, missing.Missing, 1)
	assert.Equal(t, child, missing.Missing[0].URN)
	assert.Equal(t, "parent", missing.Missing[0].Kind)
	assert.Equal(t, string(parent), missing.Missing[0].Reference)
	assert.Contains(t, err.Error(), string(child))
	assert.Contains(t, err.Error(), string(parent))
}

// The records assemble in an order where each resource follows what it refers to, whatever order the keys sort in.
func TestAssembleOrdersParentsFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newCountingStore(t)
	stackURN := resource.URN("urn:pulumi:test::test::pulumi:pulumi:Stack::test-test")
	a := resource.URN("urn:pulumi:test::test::pkgA:m:typA::a")
	b := resource.URN("urn:pulumi:test::test::pkgA:m:typA::b")

	l, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	p := newRecordPersister(ctx, store, l, Generation{}, true, nil)
	require.NoError(t, p.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{Resources: []apitype.ResourceV3{
		{URN: stackURN, Type: resource.RootStackType, Outputs: map[string]any{"out": "x"}},
		{URN: b, Type: "pkgA:m:typA", Parent: stackURN},
		{URN: a, Type: "pkgA:m:typA", Parent: stackURN, Dependencies: []resource.URN{b}},
	}}}))

	l, err = load(ctx, store, testKeys)
	require.NoError(t, err)
	res := l.deployment.Deployment.Resources
	require.Len(t, res, 3)
	assert.Equal(t, stackURN, res[0].URN)
	assert.Equal(t, b, res[1].URN)
	assert.Equal(t, a, res[2].URN)
	assert.Equal(t, map[string]any{"out": "x"}, res[0].Outputs, "the output record is merged back into the Stack")

	// The output is its own record.
	_, version, exists, err := store.Get(ctx, testKeys.output("out"))
	require.NoError(t, err)
	assert.True(t, exists)
	assert.NotEmpty(t, version)
}

// Another run's operation in flight on a resource this run does not touch stays in that resource's record. The
// snapshot manager drops a base snapshot's pending updates, and writing that drop back would erase the marker and
// make the other run's own write fail.
func TestAnotherRunsPendingOperationIsLeftAlone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newCountingStore(t)
	urnX := resource.URN("urn:pulumi:test::test::pkgA:m:typA::resX")
	urnY := resource.URN("urn:pulumi:test::test::pkgA:m:typA::resY")
	x := apitype.ResourceV3{URN: urnX, Type: "pkgA:m:typA", Custom: true, ID: "x-1"}
	y := apitype.ResourceV3{URN: urnY, Type: "pkgA:m:typA", Custom: true, ID: "y-1"}

	// The other run has written its pending update on resX.
	l, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	other := newRecordPersister(ctx, store, l, Generation{}, true, nil)
	require.NoError(t, other.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
		Resources:         []apitype.ResourceV3{x, y},
		PendingOperations: []apitype.OperationV2{{Resource: x, Type: apitype.OperationTypeUpdating}},
	}}))

	// This run read that, and changes resY only. Its deployment no longer carries the pending update on resX.
	l, err = load(ctx, store, testKeys)
	require.NoError(t, err)
	before := store.writes[testKeys.resource(urnX)]
	mine := newRecordPersister(ctx, store, l, Generation{}, true, nil)
	y2 := y
	y2.Outputs = map[string]any{"v": "2"}
	require.NoError(t, mine.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
		Resources: []apitype.ResourceV3{x, y2},
	}}))

	assert.Equal(t, before, store.writes[testKeys.resource(urnX)], "resX's record was rewritten")
	l, err = load(ctx, store, testKeys)
	require.NoError(t, err)
	require.Len(t, l.deployment.Deployment.PendingOperations, 1)
	assert.Equal(t, urnX, l.deployment.Deployment.PendingOperations[0].Resource.URN)
}
