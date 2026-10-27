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

// The acceptance tests of INTENTIUS/plunge#7, against the local record store, a DIY backend in a temp dir and the
// in-process engine with deploytest providers standing in for pulumi-aws:
//
//   - adoption is gated: a stock checkpoint with no records refuses every run, and an unconfirmed adoption writes
//     nothing;
//   - adoption is recorded, and a re-run is a no-op (no write reaches the store);
//   - backing out to stock leaves only marker-tag removals under `preview --refresh`;
//   - a provider's ignoreTags matching the marker prefix is refused by name;
//   - a create-only-tag type is refused for adoption, and its existing resources are never marked (never replaced);
//   - a collision is refused naming both IDs, and a single marked object is bound;
//   - a saved plan whose records moved is refused with exit code 3.
//
// Written for P-L4 and not run in this unit; the maintainer runs them.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/diy"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/engine"
	lt "github.com/pulumi/pulumi/pkg/v3/engine/lifecycletest/framework"
	pkgresource "github.com/pulumi/pulumi/pkg/v3/resource"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/pkg/v3/secrets/b64"
	"github.com/pulumi/pulumi/pkg/v3/util/cancel"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
)

const bucketType = "aws:s3/bucket:Bucket"

// awsMapping is a bridged provider's GetMapping("terraform") payload, cut down to two types: a bucket whose tags
// are an updatable map, and a thing whose tags force a new resource.
const awsMapping = `{
  "provider": {"resources": {
    "aws_s3_bucket": {"bucket": {"type": 4, "optional": true}, "tags": {"type": 6, "optional": true, "element": {"schema": {"type": 4}}}},
    "aws_thing": {"tags": {"type": 6, "optional": true, "forceNew": true, "element": {"schema": {"type": 4}}}}
  }},
  "resources": {
    "aws_s3_bucket": {"tok": "aws:s3/bucket:Bucket", "fields": {}},
    "aws_thing": {"tok": "aws:m/thing:Thing", "fields": {}}
  }
}`

// awsLoaders is a stand-in for pulumi-aws: a bridged mapping, creates that return the inputs as state, in-place
// updates, and the default Read, which echoes the state (so a refresh finds the cloud as last written).
func awsLoaders() []*deploytest.ProviderLoader {
	return []*deploytest.ProviderLoader{
		deploytest.NewProviderLoader("aws", semver.MustParse("7.0.0"), func() (plugin.Provider, error) {
			return awsProvider(), nil
		}),
	}
}

func awsProvider() *deploytest.Provider {
	var mu sync.Mutex
	next := 0
	return &deploytest.Provider{
		Version: semver.MustParse("7.0.0"),
		GetMappingF: func(_ context.Context, req plugin.GetMappingRequest) (plugin.GetMappingResponse, error) {
			if req.Key != "terraform" {
				return plugin.GetMappingResponse{}, nil
			}
			return plugin.GetMappingResponse{Provider: "aws", Data: []byte(awsMapping)}, nil
		},
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
	}
}

// bucketProgram registers one bucket with a user tag.
func bucketProgram() deploytest.LanguageRuntimeFactory {
	return deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		_, err := monitor.RegisterResource(bucketType, "logs", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{
				"bucket": "logs",
				"tags":   map[string]any{"team": "a"},
			}),
		})
		return err
	})
}

// runConfig selects what one in-process run does.
type runConfig struct {
	markers bool              // set Target.Markers and Target.Binder, as the plunge backend does
	listers map[Family]Lister // the bind phase's listers; nil binds against nothing
	refresh bool              // refresh before the update (preview --refresh)
	dryRun  bool              // a preview: nothing is written
	persist bool              // write records through the record persister
	base    *deploy.Snapshot  // the snapshot to run against in place of the records' (a stock back-out run)
	marks   *ledger           // receives the run's ledger, when set
	vet     bool              // set Target.DeleteVetter, as the plunge backend does (deletevet.go)
	destroy bool              // run a destroy in place of an update
}

// run is one engine run against the records in store, returning the engine's events.
func run(
	t *testing.T, store staterecord.Store, hostF deploytest.PluginHostFactory, cfg runConfig,
) ([]engine.Event, error) {
	t.Helper()
	ctx := context.Background()

	l, err := load(ctx, store, testKeys)
	if err != nil {
		return nil, err
	}
	snap := cfg.base
	if snap == nil && len(l.records) > 0 {
		snap, err = stack.DeserializeDeploymentV3(ctx, *l.deployment.Deployment, stack.Base64SecretsProvider{})
		if err != nil {
			return nil, err
		}
	}

	p := &lt.TestPlan{}
	project := p.GetProject()
	// The target and the snapshot manager share one snapshot, as in backend.go's apply: the manager tracks the
	// base states by identity, and GetTarget's clone would leave every base state in the saved snapshot twice.
	target := p.GetTarget(t, nil)
	target.Snapshot = snap
	target.Config = config.Map{}
	marks := cfg.marks
	if cfg.markers {
		if marks == nil {
			marks = newLedger()
		}
		cat := newCatalog()
		target.ProviderIDs = DeterministicProviderID
		target.Markers = newMarkerPolicy(cat, snap, marks, nil)
		listers := cfg.listers
		if listers == nil {
			listers = map[Family]Lister{}
		}
		target.Binder = newBinder(cat, marks, listers)
		target.Autonames = newAutonamePolicy(cat, marks)
	}
	if cfg.vet {
		target.DeleteVetter = newDeleteVetter(newCatalog(), l)
	}

	var manager *backend.SnapshotManager
	if cfg.persist && !cfg.dryRun {
		sm := b64.NewBase64SecretsManager()
		persister := newRecordPersister(ctx, store, l, Generation{Commit: "c1", Time: 100}, !cfg.destroy,
			sm.Decrypter())
		persister.ownership = marks
		manager = backend.NewSnapshotManager(persister, sm, snap, nil)
	}

	host := hostF()

	events := make(chan engine.Event)
	var got []engine.Event
	drained := make(chan struct{})
	go func() {
		for e := range events {
			got = append(got, e)
		}
		close(drained)
	}()

	cancelCtx, cancelSrc := cancel.NewContext(ctx)
	defer cancelSrc.Terminate()
	// Close the host before Terminate: the engine also closes it once the cancel source terminates, and two
	// concurrent deploytest pluginHost.Close calls both send on its stop channel, so one blocks for ever.
	defer contract.IgnoreClose(host)
	engineCtx := &engine.Context{
		Cancel:        cancelCtx,
		Events:        events,
		PluginManager: lt.NopPluginManager{},
	}
	if manager != nil {
		engineCtx.SnapshotManager = manager
	}
	opts := engine.UpdateOptions{
		Parallel: 4,
		Refresh:  cfg.refresh,
		HostFactory: func(context.Context, diag.Sink, diag.Sink, plugin.DebugContext) (plugin.Host, error) {
			return host, nil
		},
	}
	var updateErr error
	if cfg.destroy {
		_, _, updateErr = engine.Destroy(lt.NewUpdateInfo(project, target), engineCtx, opts, cfg.dryRun)
	} else {
		_, _, updateErr = engine.Update(lt.NewUpdateInfo(project, target), engineCtx, opts, cfg.dryRun)
	}
	var closeErr error
	if manager != nil {
		closeErr = manager.Close()
	}
	close(events)
	<-drained
	return got, errors.Join(updateErr, closeErr)
}

// steps is every non-internal step event's metadata, by URN name.
func steps(events []engine.Event) map[string][]engine.StepEventMetadata {
	out := map[string][]engine.StepEventMetadata{}
	for _, e := range events {
		if e.Type != engine.ResourcePreEvent {
			continue
		}
		p, ok := e.Payload().(engine.ResourcePreEventPayload)
		if !ok || p.Internal {
			continue
		}
		out[p.Metadata.URN.Name()] = append(out[p.Metadata.URN.Name()], p.Metadata)
	}
	return out
}

func stringTags(v resource.PropertyValue) map[string]string {
	out := map[string]string{}
	v = unwrapValue(v)
	if !v.IsObject() {
		return out
	}
	for k, e := range v.ObjectValue() {
		if e = unwrapValue(e); e.IsString() {
			out[string(k)] = e.StringValue()
		}
	}
	return out
}

func bucketURN() resource.URN {
	return resource.NewURN("test", "test", "", tokens.Type(bucketType), "logs")
}

// The injector puts the markers in the bucket's tags, next to the program's own, and the record says so.
func TestMarkersAreInjectedIntoMapTags(t *testing.T) {
	t.Parallel()

	store := newCountingStore(t)
	marks := newLedger()
	_, err := run(t, store, deploytest.NewPluginHostF(nil, nil, bucketProgram(), nil, nil, awsLoaders()...),
		runConfig{markers: true, persist: true, marks: marks})
	require.NoError(t, err)

	want, err := markers.For(bucketURN(), markers.EncodingTags)
	require.NoError(t, err)
	tags := stringTags(byName(assembled(t, store))["logs"]["tags"])
	assert.Equal(t, "a", tags["team"])
	for k, v := range want {
		assert.Equal(t, v, tags[k], k)
	}

	l, err := load(context.Background(), store, testKeys)
	require.NoError(t, err)
	rec := l.records[testKeys.resource(bucketURN())].resource
	require.NotNil(t, rec.Ownership)
	assert.Equal(t, OwnershipMarker, rec.Ownership.By)
	assert.Equal(t, MarkerInjected, rec.Ownership.State)
	assert.Equal(t, "aws_s3_bucket", rec.Ownership.TFType)
}

// Backing out: the records are exported and run by stock Pulumi (no markers) as `preview --refresh`. The only
// change is the bucket's tags, and the only difference in them is the plunge-* keys going away.
func TestBackOutLeavesOnlyMarkerTagRemovals(t *testing.T) {
	t.Parallel()

	store := newCountingStore(t)
	hostF := deploytest.NewPluginHostF(nil, nil, bucketProgram(), nil, nil, awsLoaders()...)
	_, err := run(t, store, hostF, runConfig{markers: true, persist: true})
	require.NoError(t, err)

	// Stock Pulumi gets the export, as `plunge stack export | pulumi stack import` would give it.
	exported := assembled(t, store)
	events, err := run(t, store, deploytest.NewPluginHostF(nil, nil, bucketProgram(), nil, nil, awsLoaders()...),
		runConfig{base: exported, refresh: true, dryRun: true})
	require.NoError(t, err)

	byRes := steps(events)
	for name, ms := range byRes {
		for _, m := range ms {
			switch m.Op {
			case deploy.OpSame, deploy.OpRefresh:
				continue
			case deploy.OpUpdate:
				require.Equal(t, "logs", name, "only the marked bucket may change")
				assert.Equal(t, []resource.PropertyKey{"tags"}, m.Diffs)
				require.NotNil(t, m.Old)
				require.NotNil(t, m.New)
				before, after := stringTags(m.Old.Inputs["tags"]), stringTags(m.New.Inputs["tags"])
				var removed []string
				for k := range before {
					if _, kept := after[k]; !kept {
						removed = append(removed, k)
					}
				}
				sort.Strings(removed)
				for _, k := range removed {
					assert.True(t, strings.HasPrefix(k, markers.Prefix), "only marker tags may go: %s", k)
				}
				assert.NotEmpty(t, removed)
				for k, v := range after {
					assert.Equal(t, before[k], v, "a kept tag must not change: %s", k)
				}
				assert.Len(t, after, len(before)-len(removed), "nothing may be added")
			default:
				t.Errorf("backing out may only remove marker tags, but %s has a %s step", name, m.Op)
			}
		}
	}
	assert.NotEmpty(t, byRes["logs"])
}

// A provider configured to ignore tags with the marker prefix is refused by name, before anything is created.
func TestIgnoreTagsMatchingMarkerPrefixIsRefused(t *testing.T) {
	t.Parallel()

	urn := resource.NewURN("test", "test", "", "pulumi:providers:aws", "explicit")
	for name, inputs := range map[string]resource.PropertyMap{
		"object prefix": resource.NewPropertyMapFromMap(map[string]any{
			"ignoreTags": map[string]any{"keyPrefixes": []any{"plunge"}},
		}),
		"json string key": resource.NewPropertyMapFromMap(map[string]any{
			"ignoreTags": `{"keys":["plunge-urn"]}`,
		}),
		"default tags": resource.NewPropertyMapFromMap(map[string]any{
			"defaultTags": map[string]any{"tags": map[string]any{"plunge-stack": "x"}},
		}),
	} {
		err := checkProviderInputs(urn, inputs)
		var hidden *HiddenMarkersError
		require.ErrorAs(t, err, &hidden, name)
		assert.Equal(t, urn, hidden.Provider, name)
		assert.Contains(t, err.Error(), string(urn), name)
	}
	assert.NoError(t, checkProviderInputs(urn, resource.NewPropertyMapFromMap(map[string]any{
		"ignoreTags": map[string]any{"keyPrefixes": []any{"kubernetes.io/"}},
	})))

	// Through the engine: the explicit provider is refused, and nothing is created.
	store := newCountingStore(t)
	prog := deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		_, err := monitor.RegisterResource("pulumi:providers:aws", "explicit", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{
				"ignoreTags": map[string]any{"keyPrefixes": []any{"plunge-"}},
			}),
		})
		return err
	})
	_, err := run(t, store, deploytest.NewPluginHostF(nil, nil, prog, nil, nil, awsLoaders()...),
		runConfig{markers: true, persist: true})
	require.Error(t, err)
	l, lerr := load(context.Background(), store, testKeys)
	require.NoError(t, lerr)
	for _, k := range l.records {
		if k.resource != nil {
			assert.NotEqual(t, "explicit", k.resource.URN.Name(), "the refused provider must not be recorded")
		}
	}
}

// An existing resource whose tags force a new resource is never marked: the injector refuses rather than let the
// provider replace it.
func TestCreateOnlyTagsAreRefusedNeverReplaced(t *testing.T) {
	t.Parallel()

	thingURN := resource.NewURN("test", "test", "", "aws:m/thing:Thing", "t")
	base := &deploy.Snapshot{Resources: []*pkgresource.State{{
		URN: thingURN, Type: "aws:m/thing:Thing", Custom: true, ID: "t-1",
		Inputs: resource.NewPropertyMapFromMap(map[string]any{"tags": map[string]any{"team": "a"}}),
	}}}
	prov := &deploytest.Provider{
		Version: semver.MustParse("7.0.0"),
		GetMappingF: func(context.Context, plugin.GetMappingRequest) (plugin.GetMappingResponse, error) {
			return plugin.GetMappingResponse{Provider: "aws", Data: []byte(awsMapping)}, nil
		},
	}
	pol := newMarkerPolicy(newCatalog(), base, newLedger(), nil)
	_, err := pol.Inject(context.Background(), deploy.MarkerRequest{
		URN: thingURN, Type: "aws:m/thing:Thing", Provider: prov,
		Properties: resource.NewPropertyMapFromMap(map[string]any{"tags": map[string]any{"team": "a"}}),
	})
	var createOnly *CreateOnlyTagsError
	require.ErrorAs(t, err, &createOnly)
	assert.Equal(t, thingURN, createOnly.URN)

	// A new resource of the same type is marked at create, which replaces nothing.
	fresh := resource.NewURN("test", "test", "", "aws:m/thing:Thing", "fresh")
	out, err := pol.Inject(context.Background(), deploy.MarkerRequest{
		URN: fresh, Type: "aws:m/thing:Thing", Provider: prov, Properties: resource.PropertyMap{},
	})
	require.NoError(t, err)
	assert.Contains(t, stringTags(out["tags"]), markers.TagURN)

	// Adoption refuses an aws-native type whose tags are create-only (apprunner:Service, tagUpdatable: false),
	// from the embedded v1.83.1 metadata, before writing anything.
	f := newAdoptFixture(t, stockDeployment(t, true))
	_, err = f.backend.Adopt(context.Background(), f.stack, AdoptOptions{Confirmed: true})
	var refused *AdoptRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, err.Error(), "aws-native:apprunner:Service")
	assert.Zero(t, f.store.total(), "a refused adoption writes nothing")
}

// fakeLister answers every listing with the objects it holds.
type fakeLister struct{ objs []LiveObject }

func (f *fakeLister) Covers(TypeInfo) (bool, string) { return true, "" }
func (f *fakeLister) Consistent() bool               { return true }
func (f *fakeLister) ListMarked(
	_ context.Context, _ TypeInfo, _ deploy.BindRequest, want map[string]string,
) ([]LiveObject, error) {
	return filterMarked(f.objs, want), nil
}

// Two live objects carrying one resource's markers are a collision, refused naming both; one is bound.
func TestCollisionIsRefusedNamingBothIDs(t *testing.T) {
	t.Parallel()

	want, err := markers.For(bucketURN(), markers.EncodingTags)
	require.NoError(t, err)
	marked := func(id string) LiveObject {
		tags := map[string]string{"team": "a"}
		for k, v := range want {
			tags[k] = v
		}
		return LiveObject{ID: resource.ID(id), Tags: tags}
	}

	store := newCountingStore(t)
	two := map[Family]Lister{FamilyAWS: &fakeLister{objs: []LiveObject{marked("logs-a"), marked("logs-b")}}}
	_, err = run(t, store, deploytest.NewPluginHostF(nil, nil, bucketProgram(), nil, nil, awsLoaders()...),
		runConfig{markers: true, persist: true, listers: two})
	require.Error(t, err)

	// The binder's own refusal names both IDs.
	b := newBinder(newCatalog(), newLedger(), two)
	prov := awsProvider()
	inputs, _ := mergeMarkers(resource.NewPropertyMapFromMap(map[string]any{"tags": map[string]any{"team": "a"}}),
		TypeInfo{Family: FamilyAWS, Property: "tags", Shape: ShapeMap}, want)
	_, err = b.Bind(context.Background(), deploy.BindRequest{
		URN: bucketURN(), Type: bucketType, Provider: prov, Inputs: inputs,
	})
	var collision *CollisionError
	require.ErrorAs(t, err, &collision)
	assert.Equal(t, []resource.ID{"logs-a", "logs-b"}, collision.IDs)
	assert.Contains(t, err.Error(), "logs-a")
	assert.Contains(t, err.Error(), "logs-b")

	// One marked object: the create binds to it (an import), and nothing new is created.
	one := map[Family]Lister{FamilyAWS: &fakeLister{objs: []LiveObject{marked("logs-a"), {ID: "other"}}}}
	id, err := newBinder(newCatalog(), newLedger(), one).Bind(context.Background(), deploy.BindRequest{
		URN: bucketURN(), Type: bucketType, Provider: prov, Inputs: inputs,
	})
	require.NoError(t, err)
	assert.Equal(t, resource.ID("logs-a"), id)
}

// adoptFixture is a DIY backend in a temp dir holding a stock checkpoint, and the plunge backend over it.
type adoptFixture struct {
	backend *plungeBackend
	stack   backend.Stack
	store   *countingStore
}

func newAdoptFixture(t *testing.T, d *apitype.UntypedDeployment) *adoptFixture {
	t.Helper()
	ctx := context.Background()
	sink := diag.DefaultSink(io.Discard, io.Discard, diag.FormatOptions{Color: colors.Never})
	dir := t.TempDir()
	url := "file://" + filepath.ToSlash(dir)
	inner, err := diy.New(ctx, sink, url, nil)
	require.NoError(t, err)
	ref, err := inner.ParseStackReference("organization/test/test")
	require.NoError(t, err)
	s, err := inner.CreateStack(ctx, ref, "", nil, nil)
	require.NoError(t, err)
	if d != nil {
		require.NoError(t, inner.ImportDeployment(ctx, s, d))
	}
	store := newCountingStore(t)
	b := newBackend(sink, URLPrefix+url, inner, store)
	return &adoptFixture{backend: b, stack: b.wrap(s), store: store}
}

// stockDeployment is a stock checkpoint: the root Stack, the default aws provider, one bucket; with createOnly, an
// aws-native provider at the embedded metadata's version and an App Runner service too.
func stockDeployment(t *testing.T, createOnly bool) *apitype.UntypedDeployment {
	t.Helper()
	stackURN := resource.NewURN("test", "test", "", resource.RootStackType, "test-test")
	awsProv := resource.NewURN("test", "test", "", "pulumi:providers:aws", "default_7_0_0")
	resources := []apitype.ResourceV3{
		{URN: stackURN, Type: resource.RootStackType, Custom: false},
		{URN: awsProv, Type: "pulumi:providers:aws", Custom: true, ID: "prov-1",
			Inputs: map[string]any{"version": "7.0.0"}, Outputs: map[string]any{"version": "7.0.0"}},
		{URN: bucketURN(), Type: bucketType, Custom: true, ID: "logs", Parent: stackURN,
			Provider: string(awsProv) + "::prov-1",
			Inputs:   map[string]any{"bucket": "logs", "tags": map[string]any{"team": "a"}},
			Outputs:  map[string]any{"bucket": "logs", "tags": map[string]any{"team": "a"}}},
	}
	if createOnly {
		nativeProv := resource.NewURN("test", "test", "", "pulumi:providers:aws-native", "default_1_83_1")
		resources = append(resources,
			apitype.ResourceV3{URN: nativeProv, Type: "pulumi:providers:aws-native", Custom: true, ID: "prov-2",
				Inputs: map[string]any{"version": "1.83.1"}, Outputs: map[string]any{"version": "1.83.1"}},
			apitype.ResourceV3{
				URN:  resource.NewURN("test", "test", "", "aws-native:apprunner:Service", "svc"),
				Type: "aws-native:apprunner:Service", Custom: true, ID: "arn:aws:apprunner:svc", Parent: stackURN,
				Provider: string(nativeProv) + "::prov-2",
				Inputs:   map[string]any{"tags": []any{map[string]any{"key": "team", "value": "a"}}},
				Outputs:  map[string]any{"tags": []any{map[string]any{"key": "team", "value": "a"}}},
			})
	}
	// A checkpoint the stock CLI wrote carries the magic cookie its integrity check expects.
	manifest := deploy.Manifest{Version: "test"}
	manifest.Magic = manifest.NewMagic()
	dep := apitype.DeploymentV3{Manifest: manifest.Serialize(), Resources: resources}
	raw, err := json.Marshal(dep)
	require.NoError(t, err)
	return &apitype.UntypedDeployment{Version: 3, Deployment: raw}
}

// Adoption is gated: a stock checkpoint with no records refuses runs, and an unconfirmed adoption writes nothing.
func TestAdoptionIsGated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	f := newAdoptFixture(t, stockDeployment(t, false))
	l, err := load(ctx, f.store, mustKeys(t, f.stack))
	require.NoError(t, err)
	err = f.backend.requireAdopted(ctx, f.stack, l, stack.Base64SecretsProvider{})
	var required *AdoptionRequiredError
	require.ErrorAs(t, err, &required)
	assert.Equal(t, 1, required.Resources)

	_, err = f.backend.Adopt(ctx, f.stack, AdoptOptions{})
	var unconfirmed *AdoptUnconfirmedError
	require.ErrorAs(t, err, &unconfirmed)
	assert.Zero(t, f.store.total(), "an unconfirmed adoption writes nothing")

	// A stack plunge created itself, with no stock checkpoint, needs no adoption.
	empty := newAdoptFixture(t, nil)
	l, err = load(ctx, empty.store, mustKeys(t, empty.stack))
	require.NoError(t, err)
	assert.NoError(t, empty.backend.requireAdopted(ctx, empty.stack, l, stack.Base64SecretsProvider{}))
}

// Adoption is recorded with the checkpoint's IDs, and a second adoption of the same checkpoint writes nothing.
func TestAdoptionIsRecordedAndReRunIsANoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	f := newAdoptFixture(t, stockDeployment(t, false))
	res, err := f.backend.Adopt(ctx, f.stack, AdoptOptions{Confirmed: true})
	require.NoError(t, err)
	assert.True(t, res.Written)

	keys := mustKeys(t, f.stack)
	l, err := load(ctx, f.store, keys)
	require.NoError(t, err)
	require.NotNil(t, l.adoption)
	assert.Equal(t, res.Digest, l.adoption.CheckpointDigest)
	var ids []resource.ID
	for _, r := range l.adoption.Resources {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []resource.ID{"logs"}, ids, "adoption keeps the checkpoint's IDs")
	rec := l.records[keys.resource(bucketURN())]
	require.NotNil(t, rec)
	assert.Equal(t, resource.ID("logs"), rec.resource.States[0].ID)

	// The gate now lets runs through.
	assert.NoError(t, f.backend.requireAdopted(ctx, f.stack, l, stack.Base64SecretsProvider{}))

	before := f.store.total()
	again, err := f.backend.Adopt(ctx, f.stack, AdoptOptions{Confirmed: true})
	require.NoError(t, err)
	assert.True(t, again.AlreadyAdopted)
	assert.False(t, again.Written)
	assert.Equal(t, before, f.store.total(), "a re-run of the adoption writes nothing")
}

func mustKeys(t *testing.T, s backend.Stack) stackKeys {
	t.Helper()
	k, err := keysFor(s.Ref())
	require.NoError(t, err)
	return k
}

// A saved plan whose records moved after it was stamped is refused, naming the record, with exit code 40.
func TestMovedPlanIsRefusedWithExitCode40(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := newCountingStore(t)
	_, err := run(t, store, deploytest.NewPluginHostF(nil, nil, bucketProgram(), nil, nil, awsLoaders()...),
		runConfig{markers: true, persist: true})
	require.NoError(t, err)

	l, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	stamp := StampPlan(l, []resource.URN{bucketURN()})
	require.NoError(t, VerifyPlan(ctx, store, testKeys, stamp))

	// Another run moves the bucket's record.
	moved := deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		_, err := monitor.RegisterResource(bucketType, "logs", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{"bucket": "logs", "tags": map[string]any{"team": "b"}}),
		})
		return err
	})
	_, err = run(t, store, deploytest.NewPluginHostF(nil, nil, moved, nil, nil, awsLoaders()...),
		runConfig{markers: true, persist: true})
	require.NoError(t, err)

	err = VerifyPlan(ctx, store, testKeys, stamp)
	var movedErr *PlanMovedError
	require.ErrorAs(t, err, &movedErr)
	assert.Equal(t, 40, movedErr.CustomExitCode())
	assert.Equal(t, 40, ExitPlanMoved)
	assert.Contains(t, err.Error(), string(bucketURN()))
}

// Unknown tags during preview are recorded, not guessed at, and the preview leaves the inputs alone.
func TestUnknownTagsDuringPreviewAreRecorded(t *testing.T) {
	t.Parallel()

	marks := newLedger()
	pol := newMarkerPolicy(newCatalog(), nil, marks, nil)
	prov := awsProvider()
	props := resource.PropertyMap{"tags": resource.MakeComputed(resource.NewProperty(""))}
	out, err := pol.Inject(context.Background(), deploy.MarkerRequest{
		URN: bucketURN(), Type: bucketType, Provider: prov, Properties: props, DryRun: true,
	})
	require.NoError(t, err)
	assert.True(t, out["tags"].IsComputed())
	e, ok := marks.get(bucketURN())
	require.True(t, ok)
	assert.Equal(t, MarkerTagsUnknown, e.State)
}
