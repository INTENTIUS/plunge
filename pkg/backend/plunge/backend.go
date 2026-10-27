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
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/display"
	"github.com/pulumi/pulumi/pkg/v3/backend/diy"
	sdkDisplay "github.com/pulumi/pulumi/pkg/v3/display"
	"github.com/pulumi/pulumi/pkg/v3/engine"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/pkg/v3/secrets"
	"github.com/pulumi/pulumi/pkg/v3/util/nosleep"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"
	"github.com/pulumi/pulumi/sdk/v3/go/common/encoding"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

// EnvMarkers turns off marker injection and the bind phase when set to "false". It exists for diagnosing a
// provider that misbehaves with the markers; a stack run with it off is not covered by the concurrency claims.
const EnvMarkers = "PLUNGE_MARKERS"

func markersEnabled() bool { return !strings.EqualFold(os.Getenv(EnvMarkers), "false") }

// EnvRefresh turns off the plunge backend's default refresh before update and preview when set to "false".
const EnvRefresh = "PLUNGE_REFRESH"

// plungeBackend is a backend.Backend that keeps a stack's state as one record per resource in a record store with
// conditional writes, and takes no stack lock. Everything that is not state (which stacks exist, tags, history,
// the default secrets manager) is the DIY backend at the same root, unchanged.
type plungeBackend struct {
	backend.Backend // the DIY backend at innerURL(url), for stack metadata

	d     diag.Sink
	url   string
	store staterecord.Store
	// newListers builds the bind phase's family listers; tests replace it to stand in for the clouds.
	newListers func() map[Family]Lister

	// previews is the records each stack's last preview in this process read, by stack reference, for the saved
	// plan's stamp (planstamp.go).
	previews sync.Map
}

func (b *plungeBackend) previewed(ref string) (*loaded, bool) {
	v, ok := b.previews.Load(ref)
	if !ok {
		return nil, false
	}
	return v.(*loaded), true
}

func (b *plungeBackend) listers() map[Family]Lister {
	if b.newListers != nil {
		return b.newListers()
	}
	return defaultListers()
}

// New opens the plunge backend a plunge+<diy url> names.
func New(ctx context.Context, d diag.Sink, plungeURL string, project *workspace.Project) (backend.Backend, error) {
	if !IsPlungeBackendURL(plungeURL) {
		return nil, fmt.Errorf("plunge: %q is not a plunge backend URL", plungeURL)
	}
	meta, err := metadataURL(plungeURL)
	if err != nil {
		return nil, err
	}
	inner, err := diy.New(ctx, d, meta, project)
	if err != nil {
		return nil, err
	}
	store, err := openStore(ctx, plungeURL)
	if err != nil {
		return nil, err
	}
	return newBackend(d, plungeURL, inner, store), nil
}

func newBackend(d diag.Sink, plungeURL string, inner backend.Backend, store staterecord.Store) *plungeBackend {
	return &plungeBackend{Backend: inner, d: d, url: plungeURL, store: store}
}

// Login opens the plunge backend and records it as the current one.
func Login(ctx context.Context, d diag.Sink, plungeURL string, project *workspace.Project) (backend.Backend, error) {
	be, err := New(ctx, d, plungeURL, project)
	if err != nil {
		return nil, err
	}
	return be, workspace.StoreAccount(be.URL(), workspace.Account{}, true)
}

// URL is the plunge URL this backend was opened with, normalized through the DIY backend's own URL where the DIY
// URL is inside it. A plunge+k8s:// URL keeps its metadata URL in a query key, so it is returned as given.
func (b *plungeBackend) URL() string {
	if isK8sURL(b.url) {
		return b.url
	}
	return URLPrefix + b.Backend.URL()
}

// keysFor is the record namespace of a stack. Records are per project, so a stack reference must name one.
func keysFor(ref backend.StackReference) (stackKeys, error) {
	project, ok := ref.Project()
	if !ok || project == "" {
		return stackKeys{}, fmt.Errorf("plunge: the stack %s names no project; the plunge backend keeps records per "+
			"project, so stacks must be project-scoped (run `plunge state upgrade` on the DIY backend first)", ref)
	}
	return newStackKeys(string(project), ref.Name().String()), nil
}

// plungeStack is a DIY stack whose state is read from records.
type plungeStack struct {
	backend.Stack
	b *plungeBackend
}

func (b *plungeBackend) wrap(s backend.Stack) backend.Stack {
	if s == nil {
		return nil
	}
	return &plungeStack{Stack: s, b: b}
}

// unwrap returns the DIY stack inside s, for the DIY backend's own methods.
func unwrap(s backend.Stack) backend.Stack {
	if ps, ok := s.(*plungeStack); ok {
		return ps.Stack
	}
	return s
}

func (s *plungeStack) Backend() backend.Backend { return s.b }

func (s *plungeStack) Snapshot(ctx context.Context, sp secrets.Provider) (*deploy.Snapshot, error) {
	_, snap, err := s.b.snapshot(ctx, s.Ref(), sp)
	return snap, err
}

func (s *plungeStack) SnapshotStackOutputs(ctx context.Context, sp secrets.Provider) (property.Map, error) {
	keys, err := keysFor(s.Ref())
	if err != nil {
		return property.Map{}, err
	}
	l, err := load(ctx, s.b.store, keys)
	if err != nil {
		return property.Map{}, err
	}
	outputs, err := stack.DeserializeStackOutputs(ctx, *l.deployment.Deployment, sp)
	if err != nil {
		return property.Map{}, err
	}
	return resource.FromResourcePropertyMap(outputs), nil
}

// snapshot reads a stack's records and turns them into a verified snapshot. A stack with no records has no
// snapshot (nil), as a new DIY stack has none.
func (b *plungeBackend) snapshot(
	ctx context.Context, ref backend.StackReference, sp secrets.Provider,
) (*loaded, *deploy.Snapshot, error) {
	keys, err := keysFor(ref)
	if err != nil {
		return nil, nil, err
	}
	l, err := load(ctx, b.store, keys)
	if err != nil {
		return nil, nil, err
	}
	if len(l.records) == 0 {
		return l, nil, nil
	}
	snap, err := stack.DeserializeDeploymentV3(ctx, *l.deployment.Deployment, sp)
	if err != nil {
		return nil, nil, err
	}
	if !backend.DisableIntegrityChecking {
		if err := snap.VerifyIntegrity(); err != nil {
			return nil, nil, fmt.Errorf("plunge: the snapshot assembled from %s's records fails integrity: %w", ref, err)
		}
	}
	return l, snap, nil
}

func (b *plungeBackend) GetStack(ctx context.Context, ref backend.StackReference) (backend.Stack, error) {
	s, err := b.Backend.GetStack(ctx, ref)
	if err != nil {
		return nil, err
	}
	return b.wrap(s), nil
}

func (b *plungeBackend) CreateStack(
	ctx context.Context, ref backend.StackReference, root string,
	initialState *apitype.UntypedDeployment, opts *backend.CreateStackOptions,
) (backend.Stack, error) {
	if _, err := keysFor(ref); err != nil {
		return nil, err
	}
	s, err := b.Backend.CreateStack(ctx, ref, root, nil, opts)
	if err != nil {
		return nil, err
	}
	ps := b.wrap(s)
	if initialState != nil {
		if err := b.ImportDeployment(ctx, ps, initialState); err != nil {
			return nil, err
		}
	}
	return ps, nil
}

func (b *plungeBackend) RemoveStack(ctx context.Context, s backend.Stack, force, removeBackups bool) (bool, error) {
	if removeBackups {
		return false, &UnsupportedOnRecordsError{What: "checkpoint backups", Ref: s.Ref().String()}
	}
	keys, err := keysFor(s.Ref())
	if err != nil {
		return false, err
	}
	l, err := load(ctx, b.store, keys)
	if err != nil {
		return false, err
	}
	if !force && len(l.deployment.Deployment.Resources) > 0 {
		return true, errors.New("refusing to remove stack because it still contains resources")
	}
	if has, err := b.Backend.RemoveStack(ctx, unwrap(s), force, removeBackups); err != nil {
		return has, err
	}
	for key, k := range l.records {
		if err := b.store.Delete(ctx, key, k.version); err != nil {
			return false, fmt.Errorf("plunge: removing the record at %q: %w", key, err)
		}
	}
	return false, nil
}

func (b *plungeBackend) RenameStack(
	ctx context.Context, s backend.Stack, newName tokens.QName,
) (backend.StackReference, error) {
	return nil, errors.New("plunge: renaming a stack is not supported by the plunge backend yet: every record is " +
		"keyed by URNs that embed the stack name, and rewriting them is a migration, not a rename")
}

func (b *plungeBackend) GetLatestConfiguration(
	ctx context.Context, s backend.Stack,
) (backend.LatestConfiguration, error) {
	return b.Backend.GetLatestConfiguration(ctx, unwrap(s))
}

func (b *plungeBackend) UpdateStackTags(
	ctx context.Context, s backend.Stack, tags map[apitype.StackTagName]string,
) error {
	return b.Backend.UpdateStackTags(ctx, unwrap(s), tags)
}

func (b *plungeBackend) GetStackDeploymentSettings(
	ctx context.Context, s backend.Stack,
) (*apitype.DeploymentSettings, error) {
	return b.Backend.GetStackDeploymentSettings(ctx, unwrap(s))
}

func (b *plungeBackend) DestroyStackDeploymentSettings(ctx context.Context, s backend.Stack) error {
	return b.Backend.DestroyStackDeploymentSettings(ctx, unwrap(s))
}

func (b *plungeBackend) GetGHAppIntegration(
	ctx context.Context, s backend.Stack,
) (*apitype.GitHubAppIntegration, error) {
	return b.Backend.GetGHAppIntegration(ctx, unwrap(s))
}

func (b *plungeBackend) CancelCurrentUpdate(ctx context.Context, ref backend.StackReference) error {
	return errors.New("plunge: the plunge backend holds no lock across a run, so there is no update to cancel; " +
		"a run that died left nothing held, and the next run starts from the records as they are")
}

func (b *plungeBackend) ExportDeployment(ctx context.Context, s backend.Stack) (*apitype.UntypedDeployment, error) {
	keys, err := keysFor(s.Ref())
	if err != nil {
		return nil, err
	}
	l, err := load(ctx, b.store, keys)
	if err != nil {
		return nil, err
	}
	data, err := encoding.JSON.Marshal(l.deployment.Deployment)
	if err != nil {
		return nil, err
	}
	return &apitype.UntypedDeployment{
		Version:    l.deployment.Version,
		Features:   l.deployment.Features,
		Deployment: json.RawMessage(data),
	}, nil
}

// ImportDeployment replaces the stack's records with the deployment's: records it implies are written (each
// conditioned on what was read), records it does not are deleted. It is the one operation that rewrites the whole
// stack, and it is still per-record CAS, never a blind overwrite.
func (b *plungeBackend) ImportDeployment(ctx context.Context, s backend.Stack, d *apitype.UntypedDeployment) error {
	keys, err := keysFor(s.Ref())
	if err != nil {
		return err
	}
	v3, err := stack.UnmarshalUntypedDeployment(ctx, d)
	if err != nil {
		return err
	}
	l, err := load(ctx, b.store, keys)
	if err != nil {
		return err
	}
	// The secrets record is immutable; importing a deployment with another secrets configuration is refused by
	// the persister, by name.
	p := newRecordPersister(ctx, b.store, l, Generation{}, false, nil)
	// An import is the whole stack's state as the user gives it, pending operations included: removing a crashed
	// run's pending operation from an export and importing it is how one is cleared.
	p.authoritative = true
	return p.Save(apitype.TypedDeployment{Deployment: v3, Version: d.Version, Features: d.Features})
}

func (b *plungeBackend) Preview(ctx context.Context, s backend.Stack,
	op backend.UpdateOperation, events chan<- engine.Event,
) (*deploy.Plan, sdkDisplay.ResourceChanges, error) {
	return backend.Preview(ctx, s, op, b.apply, events)
}

// Update, Import, Refresh and Destroy take no stack lock: every record write is its own conditional request.

func (b *plungeBackend) Update(ctx context.Context, s backend.Stack,
	op backend.UpdateOperation, events chan<- engine.Event,
) (sdkDisplay.ResourceChanges, error) {
	return backend.PreviewThenPromptThenExecute(ctx, apitype.UpdateUpdate, s, op, b.apply, nil, events)
}

func (b *plungeBackend) Import(ctx context.Context, s backend.Stack,
	op backend.UpdateOperation, imports []deploy.Import,
) (sdkDisplay.ResourceChanges, error) {
	op.Imports = imports
	if op.Opts.PreviewOnly {
		op.Opts.Engine.GeneratePlan = false
		_, changes, err := b.apply(ctx, apitype.ResourceImportUpdate, s, op,
			backend.ApplierOptions{DryRun: true, ShowLink: true}, nil)
		return changes, err
	}
	return backend.PreviewThenPromptThenExecute(ctx, apitype.ResourceImportUpdate, s, op, b.apply, nil, nil)
}

func (b *plungeBackend) Refresh(ctx context.Context, s backend.Stack,
	op backend.UpdateOperation,
) (sdkDisplay.ResourceChanges, error) {
	if op.Opts.PreviewOnly {
		op.Opts.Engine.GeneratePlan = false
		_, changes, err := b.apply(ctx, apitype.RefreshUpdate, s, op,
			backend.ApplierOptions{DryRun: true, ShowLink: true}, nil)
		return changes, err
	}
	return backend.PreviewThenPromptThenExecute(ctx, apitype.RefreshUpdate, s, op, b.apply, nil, nil)
}

func (b *plungeBackend) Destroy(ctx context.Context, s backend.Stack,
	op backend.UpdateOperation,
) (sdkDisplay.ResourceChanges, error) {
	if op.Opts.PreviewOnly {
		op.Opts.Engine.GeneratePlan = false
		_, changes, err := b.apply(ctx, apitype.DestroyUpdate, s, op,
			backend.ApplierOptions{DryRun: true, ShowLink: true}, nil)
		return changes, err
	}
	return backend.PreviewThenPromptThenExecute(ctx, apitype.DestroyUpdate, s, op, b.apply, nil, nil)
}

// programDriven reports whether kind deletes what the program did not register, which is when the delete guard
// applies. Destroy is an explicit request to delete, and refresh and import delete nothing for that reason.
func programDriven(kind apitype.UpdateKind) bool {
	return kind == apitype.UpdateUpdate || kind == apitype.PreviewUpdate
}

// apply runs one operation against the stack's records. It mirrors the DIY backend's apply, without the lock, the
// checkpoint file, the history entry or the backup.
func (b *plungeBackend) apply(
	ctx context.Context, kind apitype.UpdateKind, s backend.Stack,
	op backend.UpdateOperation, opts backend.ApplierOptions, events chan<- engine.Event,
) (*deploy.Plan, sdkDisplay.ResourceChanges, error) {
	resetKeepRunning := nosleep.KeepRunning()
	defer resetKeepRunning()

	ref := s.Ref()
	if op.CoherenceWindow != "" {
		return nil, nil, errors.New("coherence windows are only supported by the Pulumi Cloud backend")
	}

	// Refresh policy (INTENTIUS/plunge#2, decision 4): an update or preview reads live first, so a value another run
	// moved at the cloud API is what the plan diffs against and the next plan converges.
	if programDriven(kind) && !op.Opts.Engine.Refresh && !strings.EqualFold(os.Getenv(EnvRefresh), "false") {
		op.Opts.Engine.Refresh = true
	}

	l, snap, err := b.snapshot(ctx, ref, op.SecretsProvider)
	if err != nil {
		return nil, nil, err
	}
	// The adoption gate (adopt.go): a stock checkpoint at this root with no records is adopted first, never run
	// against as if it were empty.
	if err := b.requireAdopted(ctx, s, l, op.SecretsProvider); err != nil {
		return nil, nil, err
	}
	if kind == apitype.PreviewUpdate || opts.DryRun {
		b.previews.Store(ref.String(), l)
	}

	run, err := ProgramGeneration(ctx, op.Root, nil)
	if err != nil {
		return nil, nil, err
	}

	tags := map[string]string{}
	for k, v := range unwrap(s).Tags() {
		tags[k] = v
	}
	target := &deploy.Target{
		Name:         ref.Name(),
		Organization: "organization",
		Config:       op.StackConfiguration.Config,
		Decrypter:    op.StackConfiguration.Decrypter,
		Snapshot:     snap,
		Tags:         tags,
		ProviderIDs:  DeterministicProviderID,
	}
	guard := programDriven(kind)
	if guard {
		target.DeleteGuard = deleteGuard(run, l.generations)
	}
	// Ownership markers and the bind phase (INTENTIUS/plunge#7) run where the program registers resources, and the
	// autonaming policy (autoname.go, claim C1) runs before each resource's Check.
	var marks *ledger
	var listers map[Family]Lister
	cat := newCatalog()
	if programDriven(kind) && markersEnabled() {
		marks = newLedger()
		listers = b.listers()
		target.Markers = newMarkerPolicy(cat, snap, marks, b.d)
		target.Binder = newBinder(cat, marks, listers)
		target.Autonames = newAutonamePolicy(cat, marks)
	}
	// The delete vet (deletevet.go, INTENTIUS/plunge#37): the live marker decides every delete, destroy included.
	if (programDriven(kind) || kind == apitype.DestroyUpdate) && markersEnabled() {
		target.DeleteVetter = newDeleteVetter(cat, l)
	}
	update := engine.UpdateInfo{Root: op.Root, Project: op.Proj, Target: target}

	actionLabel := backend.ActionLabel(kind, opts.DryRun)
	if !op.Opts.Display.JSONDisplay && !op.Opts.Display.SummaryJSON {
		fmt.Printf(op.Opts.Display.Color.Colorize(
			colors.SpecHeadline+"%s (%s):"+colors.Reset+"\n"), actionLabel, ref)
	}

	displayEvents := make(chan engine.Event)
	displayDone := make(chan bool)
	go display.ShowEvents(
		strings.ToLower(actionLabel), kind, ref.Name(), op.Proj.Name, "",
		displayEvents, displayDone, op.Opts.Display, opts.DryRun)

	engineEvents := make(chan engine.Event)
	scope := op.Scopes.NewScope(ctx, engineEvents, opts.DryRun)
	eventsDone := make(chan bool)
	go func() {
		for e := range engineEvents {
			displayEvents <- e
			if events != nil {
				events <- e
			}
		}
		close(eventsDone)
	}()

	engineCtx := &engine.Context{
		Cancel:        scope.Context(),
		Events:        engineEvents,
		BackendClient: backend.NewBackendClient(b, op.SecretsProvider),
		SnapshotManagerCapabilities: engine.SnapshotManagerCapabilities{
			StateMigrations: true,
		},
	}
	var manager *backend.SnapshotManager
	if kind != apitype.PreviewUpdate && !opts.DryRun {
		var dec config.Decrypter
		if op.SecretsManager != nil {
			dec = op.SecretsManager.Decrypter()
		}
		persister := newRecordPersister(ctx, b.store, l, run, guard, dec)
		persister.ownership = marks
		manager = backend.NewSnapshotManager(persister, op.SecretsManager, snap, nil)
		engineCtx.SnapshotManager = manager
	}

	if op.Opts.Engine.HostFactory == nil {
		op.Opts.Engine.HostFactory = backend.DefaultHostFactory(b.GetReadOnlyCloudRegistry())
	}

	var plan *deploy.Plan
	var changes sdkDisplay.ResourceChanges
	var updateErr error
	switch kind {
	case apitype.PreviewUpdate:
		plan, changes, updateErr = engine.Update(update, engineCtx, op.Opts.Engine, true)
	case apitype.UpdateUpdate:
		plan, changes, updateErr = engine.Update(update, engineCtx, op.Opts.Engine, opts.DryRun)
	case apitype.ResourceImportUpdate:
		_, changes, updateErr = engine.Import(update, engineCtx, op.Opts.Engine, op.Imports, opts.DryRun)
	case apitype.RefreshUpdate:
		if op.Opts.Engine.RefreshProgram {
			_, changes, updateErr = engine.RefreshV2(update, engineCtx, op.Opts.Engine, opts.DryRun)
		} else {
			_, changes, updateErr = engine.Refresh(update, engineCtx, op.Opts.Engine, opts.DryRun)
		}
	case apitype.DestroyUpdate:
		if op.Opts.Engine.DestroyProgram {
			_, changes, updateErr = engine.DestroyV2(update, engineCtx, op.Opts.Engine, opts.DryRun)
		} else {
			_, changes, updateErr = engine.Destroy(update, engineCtx, op.Opts.Engine, opts.DryRun)
		}
	default:
		contract.Failf("unexpected update kind %s for the plunge backend", kind)
	}

	<-displayDone
	scope.Close()
	close(engineEvents)
	var closeErr error
	if manager != nil {
		closeErr = manager.Close()
	}
	<-eventsDone
	close(displayEvents)
	// After the bind phase and the record writes, warn about marked objects no record names (sweep.go,
	// INTENTIUS/plunge#18). It runs when a record write lost too, which is the case it exists for.
	if marks != nil {
		reportUnrecorded(ctx, b.d, b.store, l.keys, marks, listers)
	}
	if closeErr != nil {
		return plan, changes, fmt.Errorf("writing records: %w", closeErr)
	}

	return plan, changes, updateErr
}
