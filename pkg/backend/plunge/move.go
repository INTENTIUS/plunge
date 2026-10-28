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

// Moving resources to another stack of the plunge backend (INTENTIUS/plunge#56), as `plunge stack move`.
//
// A resource belongs to a stack twice over: by its record under the stack's namespace, and by the plunge-stack and
// plunge-urn markers on its live object. Delete checks read the live marker (deletevet.go), so a move rewrites both:
// each live object's markers through its provider's Update, a tags-only change, and then its record, written under
// the destination and removed from the source. The move never creates, replaces or deletes a cloud object. A
// resource whose provider would replace it to change its markers is refused before anything is written.
//
// The order makes a re-run finish an interrupted move. Markers are rewritten first (rewriting them again is the same
// Update), then every destination record is written, parents before children, then the source records are removed,
// children before parents. A run that finds a destination record holding the same object as the source record takes
// it as an earlier run's and only removes the source record.

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	pkgresource "github.com/pulumi/pulumi/pkg/v3/resource"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	deployproviders "github.com/pulumi/pulumi/pkg/v3/resource/deploy/providers"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/pkg/v3/secrets"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// Mover is the plunge backend's move of resources between two of its stacks.
type Mover interface {
	Move(ctx context.Context, src, dest backend.Stack, opts MoveOptions) (*MoveResult, error)
}

var _ Mover = (*plungeBackend)(nil)

// MoveOptions is one move.
type MoveOptions struct {
	// URNs are the source stack's resources to move.
	URNs []resource.URN
	// SecretsProvider opens each stack's secrets manager from its secrets record, when a record holds a secret.
	SecretsProvider secrets.Provider
	// Plugins is the plugin context the providers are loaded through. A move of a resource that carries markers
	// needs it; a move of record-owned resources only does not.
	Plugins *plugin.Context
	// BackendClient is what the builtin `pulumi` provider reads other stacks through. Move sets it when nil.
	BackendClient deploy.BackendClient
	// Confirmed writes. Without it the move checks everything it would check, reads each live object, asks each
	// provider for its diff, and writes nothing: no record and no marker.
	Confirmed bool
	// Now is the clock for the records' write time; time.Now when nil.
	Now func() time.Time
}

// MoveStatus is what the move did, or would do, for one resource.
type MoveStatus string

const (
	// MovePlanned is a resource an unconfirmed move would move.
	MovePlanned MoveStatus = "planned"
	// MoveMoved is a resource this run moved.
	MoveMoved MoveStatus = "moved"
	// MoveResumed is a resource an earlier, interrupted run wrote to the destination; this run removes (or, unconfirmed,
	// would remove) its source record.
	MoveResumed MoveStatus = "resumed"
	// MoveAlreadyMoved is a resource the source holds no record for and the destination does: nothing to do.
	MoveAlreadyMoved MoveStatus = "already-moved"
)

// MoveResult is a move's plan or outcome.
type MoveResult struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	// Written is true when the move wrote: its markers, records, or both.
	Written   bool            `json:"written"`
	Resources []MovedResource `json:"resources"`
	// Providers are the providers the moved resources use, as the destination holds them.
	Providers []MovedProvider `json:"providers,omitempty"`
}

// MovedResource is one resource of a move.
type MovedResource struct {
	URN            resource.URN `json:"urn"`
	DestinationURN resource.URN `json:"destinationUrn"`
	ID             resource.ID  `json:"id,omitempty"`
	Type           tokens.Type  `json:"type"`
	Status         MoveStatus   `json:"status"`
	// Ownership is "marker" when the live object carries markers the move rewrites, "record" when its record alone
	// carries ownership (an untaggable type, or a component).
	Ownership Ownership `json:"ownership"`
	// Markers are the markers the live object carries once moved.
	Markers map[string]string `json:"markers,omitempty"`
	// MarkerWrite is true when the provider's diff says the marker rewrite is an update, so the move calls Update.
	MarkerWrite bool `json:"markerWrite,omitempty"`
	// Dropped are the resource's dependencies that stay in the source; the destination record does not name them.
	Dropped []resource.URN `json:"droppedDependencies,omitempty"`
}

// MovedProvider is one provider of a move, as the destination holds it.
type MovedProvider struct {
	URN            resource.URN `json:"urn"`
	DestinationURN resource.URN `json:"destinationUrn"`
	ID             resource.ID  `json:"id"`
	// Reused is true when the destination already holds this provider with the same configuration.
	Reused bool `json:"reused"`
}

// MoveRefusal is one reason a move was refused.
type MoveRefusal struct {
	URN    resource.URN `json:"urn"`
	Reason string       `json:"reason"`
}

// MoveRefusedError refuses a move before anything is written: no marker and no record.
type MoveRefusedError struct {
	Source      string
	Destination string
	Refusals    []MoveRefusal
}

func (e *MoveRefusedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "plunge refuses to move %d resource(s) from %s to %s. Nothing was written: no marker and no record.",
		len(e.Refusals), e.Source, e.Destination)
	for _, r := range e.Refusals {
		fmt.Fprintf(&b, "\n  - %s: %s", r.URN, r.Reason)
	}
	return b.String()
}

// MoveInterruptedError is a move that stopped after it began writing. A re-run of the same move finishes it.
type MoveInterruptedError struct {
	URN resource.URN
	Err error
}

func (e *MoveInterruptedError) Error() string {
	return fmt.Sprintf("plunge: the move stopped at %s: %v. No cloud object was deleted. Run the same move again to "+
		"finish it", e.URN, e.Err)
}

func (e *MoveInterruptedError) Unwrap() error { return e.Err }

// Move implements Mover.
func (b *plungeBackend) Move(ctx context.Context, src, dest backend.Stack, opts MoveOptions) (*MoveResult, error) {
	if src == nil || dest == nil {
		return nil, errors.New("plunge: a move needs a source and a destination stack")
	}
	if _, ok := dest.Backend().(*plungeBackend); !ok {
		return nil, &NotPlungeBackendError{URL: dest.Backend().URL()}
	}
	srcSide, err := sideOf(src.Ref())
	if err != nil {
		return nil, err
	}
	destSide, err := sideOf(dest.Ref())
	if err != nil {
		return nil, err
	}
	if srcSide.keys.prefix() == destSide.keys.prefix() {
		return nil, fmt.Errorf("plunge: %s is both the source and the destination of the move", src.Ref())
	}
	if opts.BackendClient == nil {
		opts.BackendClient = backend.NewBackendClient(b, opts.SecretsProvider)
	}
	return newMover(b.store, srcSide, destSide, opts).run(ctx)
}

// moveSide is one stack of a move.
type moveSide struct {
	ref     string
	project string
	stack   string
	keys    stackKeys
}

func sideOf(ref backend.StackReference) (moveSide, error) {
	keys, err := keysFor(ref)
	if err != nil {
		return moveSide{}, err
	}
	project, _ := ref.Project()
	return moveSide{ref: ref.String(), project: string(project), stack: ref.Name().String(), keys: keys}, nil
}

// rehome is urn in this stack: the same type chain and name under this stack and project.
func (s moveSide) rehome(urn resource.URN) resource.URN {
	prefix := "urn:pulumi:" + string(urn.Stack()) + "::" + string(urn.Project()) + "::"
	return resource.URN("urn:pulumi:" + s.stack + "::" + s.project + "::" + strings.TrimPrefix(string(urn), prefix))
}

// stackValues are the TagStack values a live object of this stack may carry, in either encoding.
func (s moveSide) stackValues() []string {
	v := markers.StackValue(s.project, s.stack)
	return []string{v, markers.LabelValue(markers.TagStack, v)}
}

type mover struct {
	store staterecord.Store
	src   moveSide
	dest  moveSide
	opts  MoveOptions

	srcDec  config.Decrypter
	destDec config.Decrypter
	destEnc config.Encrypter

	reg       *deployproviders.Registry
	loaded    map[string]plugin.Provider
	status    *driftStatusServer
	refusals  []MoveRefusal
	providers map[string]*providerMove // by source provider reference
}

func newMover(store staterecord.Store, src, dest moveSide, opts MoveOptions) *mover {
	return &mover{
		store: store, src: src, dest: dest, opts: opts,
		loaded: map[string]plugin.Provider{}, providers: map[string]*providerMove{},
	}
}

// resourceMove is one resource's part of the move.
type resourceMove struct {
	urn     resource.URN
	destURN resource.URN
	key     string // the source record's key
	version string // the source record's version, for the conditional delete
	rec     *resourceRecord
	state   apitype.ResourceV3
	result  *MovedResource
	resume  bool

	// Filled for a resource that carries markers.
	st       *pkgresource.State
	provider *providerMove
	inputs   resource.PropertyMap // the checked inputs, carrying the destination's markers
}

// providerMove is one provider the moved resources use: its source state and how the destination holds it.
type providerMove struct {
	ref      providers.Reference
	state    *pkgresource.State
	destURN  resource.URN
	destID   resource.ID
	create   bool // the destination holds no record for it, so the move writes one
	written  bool
	movedRef string
	result   *MovedProvider
}

func (m *mover) refuse(urn resource.URN, format string, args ...any) {
	m.refusals = append(m.refusals, MoveRefusal{URN: urn, Reason: fmt.Sprintf(format, args...)})
}

func (m *mover) close() {
	if m.status != nil {
		m.status.close()
	}
	if m.reg != nil {
		_ = m.reg.Close()
	}
}

func (m *mover) run(ctx context.Context) (*MoveResult, error) {
	defer m.close()
	now := time.Now
	if m.opts.Now != nil {
		now = m.opts.Now
	}
	if len(m.opts.URNs) == 0 {
		return nil, errors.New("plunge: name at least one resource to move")
	}

	srcL, err := load(ctx, m.store, m.src.keys)
	if err != nil {
		return nil, fmt.Errorf("plunge: reading the source stack %s: %w", m.src.ref, err)
	}
	destL, err := load(ctx, m.store, m.dest.keys)
	if err != nil {
		return nil, fmt.Errorf("plunge: reading the destination stack %s: %w", m.dest.ref, err)
	}
	m.srcDec = m.lazyDecrypter(ctx, m.src)
	destCrypter := m.lazyCrypter(ctx, m.dest)
	m.destDec, m.destEnc = destCrypter, destCrypter

	srcRecords, srcVersions := recordsByURN(srcL)
	destRecords, _ := recordsByURN(destL)

	res := &MoveResult{Source: m.src.ref, Destination: m.dest.ref}

	// The resources to move, in the source snapshot's order: each after its parent and what it depends on.
	order := map[resource.URN]int{}
	for i, s := range srcL.deployment.Deployment.Resources {
		if _, seen := order[s.URN]; !seen {
			order[s.URN] = i
		}
	}
	moving := map[resource.URN]bool{}
	var moves []*resourceMove
	for _, urn := range m.opts.URNs {
		if moving[urn] {
			continue
		}
		moving[urn] = true
		rec, ok := srcRecords[urn]
		if !ok {
			destURN := m.dest.rehome(urn)
			if d, ok := destRecords[destURN]; ok {
				r := MovedResource{URN: urn, DestinationURN: destURN, Status: MoveAlreadyMoved, Ownership: OwnershipRecord}
				if live, ok := liveState(d); ok {
					r.ID, r.Type = live.ID, live.Type
				}
				res.Resources = append(res.Resources, r)
				continue
			}
			m.refuse(urn, "the source stack holds no record for it (looked at %q), and the destination holds none for %s",
				m.src.keys.resource(urn), destURN)
			continue
		}
		moves = append(moves, &resourceMove{
			urn: urn, destURN: m.dest.rehome(urn), key: m.src.keys.resource(urn), version: srcVersions[urn], rec: rec,
		})
	}
	for urn := range moving {
		if _, ok := srcRecords[urn]; !ok {
			delete(moving, urn)
		}
	}
	sort.SliceStable(moves, func(i, j int) bool { return order[moves[i].urn] < order[moves[j].urn] })

	srcRoot, destRoot := rootURN(srcL), rootURN(destL)
	for _, mv := range moves {
		m.check(mv, moving, srcRoot, destRoot, destRecords)
	}
	m.checkDependents(srcL, moving)
	if len(m.refusals) == 0 {
		for _, mv := range moves {
			if mv.resume {
				continue
			}
			if err := m.prepare(ctx, mv, srcRecords, destRecords); err != nil {
				return nil, err
			}
		}
	}

	for _, mv := range moves {
		res.Resources = append(res.Resources, *mv.result)
	}
	if len(m.refusals) > 0 {
		return res, &MoveRefusedError{Source: m.src.ref, Destination: m.dest.ref, Refusals: m.refusals}
	}
	provKeys := make([]string, 0, len(m.providers))
	for k := range m.providers {
		provKeys = append(provKeys, k)
	}
	sort.Strings(provKeys)
	if !m.opts.Confirmed {
		for _, k := range provKeys {
			res.Providers = append(res.Providers, *m.providers[k].result)
		}
		return res, nil
	}

	// Write. First each live object's markers, then the destination's records, parents first.
	writtenAt := now().UTC().Format(time.RFC3339)
	for i, mv := range moves {
		if mv.resume {
			continue
		}
		if err := m.write(ctx, mv, destRoot, srcRoot, moving, writtenAt); err != nil {
			res.Written = res.Written || i > 0 || mv.result.MarkerWrite
			return res, &MoveInterruptedError{URN: mv.urn, Err: err}
		}
		res.Written = true
		res.Resources[len(res.Resources)-len(moves)+i] = *mv.result
	}
	// Then the source's records, children first.
	for i := len(moves) - 1; i >= 0; i-- {
		mv := moves[i]
		if err := m.store.Delete(ctx, mv.key, mv.version); err != nil {
			var conflict *staterecord.VersionConflictError
			if errors.As(err, &conflict) {
				err = &RecordConflictError{What: "resource " + string(mv.urn), Key: mv.key, Err: conflict}
			}
			return res, &MoveInterruptedError{URN: mv.urn, Err: fmt.Errorf("removing the source record: %w", err)}
		}
		res.Written = true
		if !mv.resume {
			mv.result.Status = MoveMoved
		}
		res.Resources[len(res.Resources)-len(moves)+i] = *mv.result
	}
	for _, k := range provKeys {
		res.Providers = append(res.Providers, *m.providers[k].result)
	}
	return res, nil
}

// recordsByURN is a stack's resource records by URN, with each record's version.
func recordsByURN(l *loaded) (map[resource.URN]*resourceRecord, map[resource.URN]string) {
	recs := map[resource.URN]*resourceRecord{}
	versions := map[resource.URN]string{}
	for _, k := range l.records {
		if k.resource != nil {
			recs[k.resource.URN] = k.resource
			versions[k.resource.URN] = k.version
		}
	}
	return recs, versions
}

// rootURN is the stack's root Stack resource, "" when its records hold none.
func rootURN(l *loaded) resource.URN {
	for _, s := range l.deployment.Deployment.Resources {
		if isRootStack(s) {
			return s.URN
		}
	}
	return ""
}

// check is what one resource's record alone decides: whether it can move, and whether an earlier run moved it.
func (m *mover) check(
	mv *resourceMove, moving map[resource.URN]bool, srcRoot, destRoot resource.URN,
	destRecords map[resource.URN]*resourceRecord,
) {
	r := &MovedResource{URN: mv.urn, DestinationURN: mv.destURN, Status: MovePlanned, Ownership: OwnershipRecord}
	mv.result = r

	if len(mv.rec.Pending) > 0 {
		m.refuse(mv.urn, "its record holds a %s operation in flight; move it once that run has finished, or clear the "+
			"operation if that run crashed", mv.rec.Pending[0].Type)
		return
	}
	var live []apitype.ResourceV3
	for _, s := range mv.rec.States {
		if s.Delete {
			m.refuse(mv.urn, "its record holds a state pending delete (ID %s) from a replacement; let `plunge up` "+
				"finish the replacement first", s.ID)
			return
		}
		live = append(live, s)
	}
	if len(live) != 1 {
		m.refuse(mv.urn, "its record holds %d live states where a move takes one", len(live))
		return
	}
	mv.state = live[0]
	r.ID, r.Type = mv.state.ID, mv.state.Type

	switch {
	case isRootStack(mv.state):
		m.refuse(mv.urn, "it is the stack's root Stack resource, which every stack has its own of")
		return
	case providers.IsProviderType(mv.state.Type):
		m.refuse(mv.urn, "it is a provider; a provider moves with the resources that use it, so name those instead")
		return
	case mv.state.ViewOf != "" || len(mv.rec.Views) > 0:
		m.refuse(mv.urn, "it is a view resource or has view resources, which a move does not carry yet")
		return
	}

	switch p := mv.state.Parent; {
	case p == "" || moving[p]:
	case p == srcRoot:
		if destRoot == "" {
			m.refuse(mv.urn, "its parent is the source's root Stack resource, and %s holds no root Stack record to "+
				"parent it under; run `plunge up` on the destination once before moving into it", m.dest.ref)
			return
		}
	default:
		m.refuse(mv.urn, "its parent %s stays in the source; move the parent with it", p)
		return
	}

	for _, dep := range referencedURNs(mv.state) {
		if !moving[dep] && dep != mv.state.Parent {
			r.Dropped = append(r.Dropped, dep)
		}
	}
	sort.Slice(r.Dropped, func(i, j int) bool { return r.Dropped[i] < r.Dropped[j] })
	r.Dropped = dedupeURNs(r.Dropped)

	if d, ok := destRecords[mv.destURN]; ok {
		if dl, ok := liveState(d); ok && dl.ID == mv.state.ID && dl.Type == mv.state.Type && len(d.Pending) == 0 {
			// An earlier run wrote this record and stopped before removing the source's.
			mv.resume = true
			r.Status = MoveResumed
			r.Dropped = nil
			if d.Ownership != nil && d.Ownership.By == OwnershipMarker {
				r.Ownership, r.Markers = OwnershipMarker, d.Ownership.Marker
			}
			return
		}
		m.refuse(mv.urn, "the destination already owns %s: its record at %q holds another object (ID %s)",
			mv.destURN, m.dest.keys.resource(mv.destURN), liveID(d))
	}
}

func liveID(r *resourceRecord) resource.ID {
	if s, ok := liveState(r); ok {
		return s.ID
	}
	return ""
}

// referencedURNs is every resource a state refers to besides its provider: its parent, its dependencies, its
// property dependencies, its deleted-with resource and its replace-with resources.
func referencedURNs(s apitype.ResourceV3) []resource.URN {
	var out []resource.URN
	if s.Parent != "" {
		out = append(out, s.Parent)
	}
	out = append(out, s.Dependencies...)
	for _, deps := range s.PropertyDependencies {
		out = append(out, deps...)
	}
	if s.DeletedWith != "" {
		out = append(out, s.DeletedWith)
	}
	out = append(out, s.ReplaceWith...)
	return out
}

func dedupeURNs(urns []resource.URN) []resource.URN {
	out := urns[:0]
	for i, u := range urns {
		if i == 0 || u != urns[i-1] {
			out = append(out, u)
		}
	}
	return out
}

// checkDependents refuses a move that would leave a source resource referring to a moved one: the source's next
// snapshot would hold a dangling reference, which assembling it refuses.
func (m *mover) checkDependents(srcL *loaded, moving map[resource.URN]bool) {
	for _, s := range srcL.deployment.Deployment.Resources {
		if moving[s.URN] {
			continue
		}
		for _, ref := range referencedURNs(s) {
			if moving[ref] {
				link := "depends on"
				if ref == s.Parent {
					link = "is a child of"
				}
				m.refuse(ref, "%s %s it and stays in the source; move %s with it", s.URN, link, s.URN)
			}
		}
		if s.Provider != "" {
			if ref, err := providers.ParseReference(s.Provider); err == nil && moving[ref.URN()] {
				m.refuse(ref.URN(), "%s uses it as its provider and stays in the source", s.URN)
			}
		}
	}
}

// prepare reads what the move needs from outside the records: the provider each marked resource uses, its live
// object (whose marker must name the source or, after an interrupted run, the destination), and the provider's
// check and diff of the marker rewrite. A diff that would replace the object is refused.
func (m *mover) prepare(
	ctx context.Context, mv *resourceMove, srcRecords, destRecords map[resource.URN]*resourceRecord,
) error {
	r := mv.result
	st, err := stack.DeserializeResource(mv.state, m.srcDec)
	if err != nil {
		return fmt.Errorf("plunge: decoding the recorded state of %s: %w", mv.urn, err)
	}
	mv.st = st

	if mv.state.Custom && mv.state.Provider != "" {
		pm, ok := m.provider(ctx, mv, srcRecords, destRecords)
		if !ok {
			return nil
		}
		mv.provider = pm
	}

	inputs, want, marked, err := rehomeMarkers(st.Inputs, mv.destURN)
	if err != nil {
		m.refuse(mv.urn, "%v", err)
		return nil
	}
	if !marked {
		mv.inputs = st.Inputs
		return nil
	}
	r.Ownership, r.Markers = OwnershipMarker, want
	if mv.provider == nil {
		m.refuse(mv.urn, "its inputs carry ownership markers and its record names no provider to rewrite them through")
		return nil
	}
	p, err := m.load(ctx, mv.provider)
	if err != nil {
		m.refuse(mv.urn, "loading its provider %s: %v", mv.provider.ref, err)
		return nil
	}

	// The live object's marker: the source's, or the destination's when an earlier run rewrote it.
	status, err := m.statusServer()
	if err != nil {
		return err
	}
	read, err := p.Read(ctx, plugin.ReadRequest{
		URN: st.URN, Name: st.URN.Name(), Type: st.URN.Type(), ID: st.ID,
		Inputs: resource.FromResourcePropertyMap(st.Inputs), State: resource.FromResourcePropertyMap(st.Outputs),
		Timeout: st.CustomTimeouts.Read, ResourceStatusAddress: status.address(), ResourceStatusToken: status.token,
	})
	if err != nil && read.Status != resource.StatusPartialFailure {
		m.refuse(mv.urn, "reading the live object %s: %v", st.ID, err)
		return nil
	}
	if read.ID == "" || read.Outputs == nil {
		m.refuse(mv.urn, "its live object %s was not found; `plunge refresh` the source to drop the record instead", st.ID)
		return nil
	}
	allowed := map[string]bool{}
	for _, v := range append(m.src.stackValues(), m.dest.stackValues()...) {
		allowed[v] = true
	}
	for _, v := range markerStackValues(resource.ToResourcePropertyMap(*read.Outputs)) {
		if !allowed[v] {
			m.refuse(mv.urn, "its live object %s carries the marker %s=%s, which names neither %s nor %s", st.ID,
				markers.TagStack, v, m.src.ref, m.dest.ref)
			return nil
		}
	}

	// Check takes a random seed, as the step generator passes one; the old inputs keep any name already chosen.
	randomSeed := make([]byte, 32)
	if _, err := cryptorand.Read(randomSeed); err != nil {
		return fmt.Errorf("generating a random seed for %s: %w", mv.urn, err)
	}
	check, err := p.Check(ctx, plugin.CheckRequest{
		URN: st.URN, Name: st.URN.Name(), Type: st.URN.Type(),
		NewInputs: resource.FromResourcePropertyMap(inputs), OldInputs: resource.FromResourcePropertyMap(st.Inputs),
		OldOutputs: resource.FromResourcePropertyMap(st.Outputs), RandomSeed: randomSeed,
	})
	if err != nil {
		m.refuse(mv.urn, "the provider's check of the rewritten markers failed: %v", err)
		return nil
	}
	if len(check.Failures) > 0 {
		reasons := make([]string, len(check.Failures))
		for i, f := range check.Failures {
			reasons[i] = fmt.Sprintf("%s: %s", f.Property, f.Reason)
		}
		m.refuse(mv.urn, "the provider's check of the rewritten markers failed: %s", strings.Join(reasons, "; "))
		return nil
	}
	mv.inputs = resource.ToResourcePropertyMap(check.Properties)

	diff, err := p.Diff(ctx, plugin.DiffRequest{
		URN: st.URN, Name: st.URN.Name(), Type: st.URN.Type(), ID: st.ID,
		NewInputs: check.Properties, OldInputs: resource.FromResourcePropertyMap(st.Inputs),
		OldOutputs: resource.FromResourcePropertyMap(st.Outputs),
	})
	if err != nil {
		m.refuse(mv.urn, "the provider's diff of the rewritten markers failed: %v", err)
		return nil
	}
	if len(diff.ReplaceKeys) > 0 || diff.DeleteBeforeReplace {
		keys := make([]string, len(diff.ReplaceKeys))
		for i, k := range diff.ReplaceKeys {
			keys[i] = string(k)
		}
		m.refuse(mv.urn, "rewriting its markers would replace the live object (%s), and a move never replaces or "+
			"deletes an object; move it with `pulumi state move` and leave its markers to the destination's `up`",
			strings.Join(keys, ", "))
		return nil
	}
	r.MarkerWrite = diff.Changes != plugin.DiffNone
	return nil
}

// provider resolves the provider mv uses: its state in the source, and how the destination holds it (the same
// configuration already recorded there, reused; or a record the move writes, with plunge's derived provider ID).
func (m *mover) provider(
	ctx context.Context, mv *resourceMove, srcRecords, destRecords map[resource.URN]*resourceRecord,
) (*providerMove, bool) {
	if pm, ok := m.providers[mv.state.Provider]; ok {
		return pm, true
	}
	ref, err := providers.ParseReference(mv.state.Provider)
	if err != nil {
		m.refuse(mv.urn, "its record names the provider %q, which is not a provider reference: %v", mv.state.Provider, err)
		return nil, false
	}
	provRec, ok := srcRecords[ref.URN()]
	if !ok {
		m.refuse(mv.urn, "its provider %s has no record in the source", ref.URN())
		return nil, false
	}
	provV3, ok := providerState(provRec, ref)
	if !ok {
		m.refuse(mv.urn, "the source's record for the provider %s holds no state with ID %s", ref.URN(), ref.ID())
		return nil, false
	}
	prov, err := stack.DeserializeResource(provV3, m.srcDec)
	if err != nil {
		m.refuse(mv.urn, "decoding the recorded state of its provider %s: %v", ref.URN(), err)
		return nil, false
	}
	pm := &providerMove{ref: ref, state: prov, destURN: m.dest.rehome(ref.URN())}
	pm.result = &MovedProvider{URN: ref.URN(), DestinationURN: pm.destURN}

	if d, ok := destRecords[pm.destURN]; ok {
		for _, s := range d.States {
			if s.Delete {
				continue
			}
			ds, err := stack.DeserializeResource(s, m.destDec)
			if err != nil {
				m.refuse(mv.urn, "decoding the destination's provider %s: %v", pm.destURN, err)
				return nil, false
			}
			if ds.Inputs.DeepEquals(prov.Inputs) {
				pm.destID = s.ID
				break
			}
		}
		if pm.destID == "" {
			m.refuse(mv.urn, "the destination's provider %s is configured differently from the source's %s; give "+
				"the destination's program the same provider configuration, or move with an explicit provider",
				pm.destURN, ref.URN())
			return nil, false
		}
		pm.result.Reused = true
	} else {
		id, err := DeterministicProviderID(pm.destURN, prov.Inputs)
		if err != nil {
			m.refuse(mv.urn, "deriving the destination's provider ID: %v", err)
			return nil, false
		}
		pm.destID, pm.create = id, true
	}
	switch p := prov.Parent; {
	case p == "":
	case isRootParent(srcRecords, p):
	default:
		m.refuse(mv.urn, "its provider %s is a child of %s, which a move does not carry", ref.URN(), p)
		return nil, false
	}
	destRef, err := providers.NewReference(pm.destURN, pm.destID)
	if err != nil {
		m.refuse(mv.urn, "naming the destination's provider: %v", err)
		return nil, false
	}
	pm.movedRef = destRef.String()
	pm.result.ID = pm.destID
	m.providers[mv.state.Provider] = pm
	return pm, true
}

func isRootParent(recs map[resource.URN]*resourceRecord, urn resource.URN) bool {
	r, ok := recs[urn]
	if !ok {
		return false
	}
	s, ok := liveState(r)
	return ok && isRootStack(s)
}

// load configures the provider pm names from its source state, once per run.
func (m *mover) load(ctx context.Context, pm *providerMove) (plugin.Provider, error) {
	key := pm.ref.String()
	if p, ok := m.loaded[key]; ok {
		return p, nil
	}
	if m.opts.Plugins == nil {
		return nil, errors.New("a move of a resource that carries markers needs a plugin context to load its provider")
	}
	if m.reg == nil {
		m.reg = deployproviders.NewRegistry(m.opts.Plugins, false,
			deploy.NewBuiltinProvider(m.opts.BackendClient, m.opts.Plugins.Diag))
	}
	if err := m.reg.Same(ctx, pm.state, false); err != nil {
		return nil, err
	}
	p, ok := m.reg.GetProvider(pm.ref)
	if !ok {
		return nil, fmt.Errorf("the provider %s was configured and is not registered", pm.ref)
	}
	m.loaded[key] = p
	return p, nil
}

func (m *mover) statusServer() (*driftStatusServer, error) {
	if m.status != nil {
		return m.status, nil
	}
	s, err := startDriftStatusServer()
	if err != nil {
		return nil, err
	}
	m.status = s
	return s, nil
}

// write moves one resource: its live object's markers, then the destination's provider record if the move creates
// one, then the destination's record for it.
func (m *mover) write(
	ctx context.Context, mv *resourceMove, destRoot, srcRoot resource.URN, moving map[resource.URN]bool,
	writtenAt string,
) error {
	st := mv.st
	outputs := st.Outputs
	if mv.result.Ownership == OwnershipMarker && mv.result.MarkerWrite {
		p, err := m.load(ctx, mv.provider)
		if err != nil {
			return err
		}
		status, err := m.statusServer()
		if err != nil {
			return err
		}
		resp, err := p.Update(ctx, plugin.UpdateRequest{
			URN: st.URN, Name: st.URN.Name(), Type: st.URN.Type(), ID: st.ID,
			OldInputs: resource.FromResourcePropertyMap(st.Inputs), OldOutputs: resource.FromResourcePropertyMap(st.Outputs),
			NewInputs: resource.FromResourcePropertyMap(mv.inputs), Timeout: st.CustomTimeouts.Update,
			ResourceStatusAddress: status.address(), ResourceStatusToken: status.token,
		})
		if err != nil {
			return fmt.Errorf("the provider's update of the markers on %s: %w", st.ID, err)
		}
		if resp.Status == resource.StatusPartialFailure {
			return fmt.Errorf("the provider's update of the markers on %s partly failed", st.ID)
		}
		outputs = resource.ToResourcePropertyMap(resp.Properties)
	} else if mv.result.Ownership == OwnershipMarker {
		// The provider takes the rewrite as no change (it keeps tags out of its diff), so nothing is called; the
		// record still carries the destination's markers.
		if out, _, _, err := rehomeMarkers(outputs, mv.destURN); err == nil {
			outputs = out
		}
	}

	if pm := mv.provider; pm != nil && pm.create && !pm.written {
		if err := m.writeProvider(ctx, pm, srcRoot, destRoot, writtenAt, mv.rec.Generation); err != nil {
			return err
		}
	}

	st.URN = mv.destURN
	st.Inputs, st.Outputs = mv.inputs, outputs
	st.Aliases = nil
	st.Parent = m.moveRef(st.Parent, srcRoot, destRoot, moving)
	if mv.provider != nil {
		st.Provider = mv.provider.movedRef
	}
	st.Dependencies = m.moveRefs(st.Dependencies, moving)
	if len(st.PropertyDependencies) > 0 {
		deps := map[resource.PropertyKey][]resource.URN{}
		for k, v := range st.PropertyDependencies {
			if moved := m.moveRefs(v, moving); len(moved) > 0 {
				deps[k] = moved
			}
		}
		st.PropertyDependencies = deps
	}
	st.DeletedWith = m.moveRef(st.DeletedWith, "", "", moving)
	st.ReplaceWith = m.moveRefs(st.ReplaceWith, moving)

	v3, _, err := stack.SerializeResource(ctx, st, m.destEnc, false)
	if err != nil {
		return fmt.Errorf("encoding the destination's state of %s: %w", mv.destURN, err)
	}
	rec := &resourceRecord{
		Format: resourceFormat, URN: mv.destURN, States: []apitype.ResourceV3{v3}, Features: mv.rec.Features,
		Generation: mv.rec.Generation, WrittenAt: writtenAt,
	}
	if o := mv.rec.Ownership; o != nil {
		moved := *o
		if mv.result.Ownership == OwnershipMarker {
			moved.Marker = mv.result.Markers
		}
		rec.Ownership = &moved
	}
	return m.put(ctx, m.dest.keys.resource(mv.destURN), "resource "+string(mv.destURN), rec)
}

// writeProvider writes the destination's record for a provider it does not hold yet.
func (m *mover) writeProvider(
	ctx context.Context, pm *providerMove, srcRoot, destRoot resource.URN, writtenAt string, gen Generation,
) error {
	st := pm.state.Copy()
	st.URN, st.ID = pm.destURN, pm.destID
	st.Aliases = nil
	if st.Parent != "" {
		st.Parent = m.moveRef(st.Parent, srcRoot, destRoot, nil)
	}
	st.Dependencies, st.PropertyDependencies, st.DeletedWith, st.ReplaceWith = nil, nil, "", nil
	v3, _, err := stack.SerializeResource(ctx, st, m.destEnc, false)
	if err != nil {
		return fmt.Errorf("encoding the destination's provider %s: %w", pm.destURN, err)
	}
	rec := &resourceRecord{
		Format: resourceFormat, URN: pm.destURN, States: []apitype.ResourceV3{v3}, Generation: gen, WrittenAt: writtenAt,
	}
	key := m.dest.keys.resource(pm.destURN)
	err = m.put(ctx, key, "provider "+string(pm.destURN), rec)
	var conflict *RecordConflictError
	if errors.As(err, &conflict) {
		// Another run created it first; it is ours to use when it holds the same provider ID.
		payload, _, exists, gerr := m.store.Get(ctx, key)
		if gerr == nil && exists {
			var theirs resourceRecord
			if decodeRecord(key, payload, resourceFormat, &theirs) == nil {
				for _, s := range theirs.States {
					if s.ID == pm.destID && !s.Delete {
						err = nil
					}
				}
			}
		}
	}
	if err != nil {
		return err
	}
	pm.written = true
	return nil
}

// put creates a destination record. It never overwrites one.
func (m *mover) put(ctx context.Context, key, what string, rec *resourceRecord) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("plunge: encoding the record for %s: %w", what, err)
	}
	if _, err := m.store.PutIfAbsent(ctx, key, payload); err != nil {
		var conflict *staterecord.VersionConflictError
		if errors.As(err, &conflict) {
			return &RecordConflictError{What: what, Key: key, Err: conflict}
		}
		return fmt.Errorf("plunge: writing the record for %s at %q: %w", what, key, err)
	}
	return nil
}

// moveRef is a reference in the destination: a moved resource's destination URN, the destination's root for the
// source's root, and "" for anything that stays.
func (m *mover) moveRef(urn, srcRoot, destRoot resource.URN, moving map[resource.URN]bool) resource.URN {
	switch {
	case urn == "":
		return ""
	case srcRoot != "" && urn == srcRoot:
		return destRoot
	case moving[urn]:
		return m.dest.rehome(urn)
	}
	return ""
}

func (m *mover) moveRefs(urns []resource.URN, moving map[resource.URN]bool) []resource.URN {
	var out []resource.URN
	for _, u := range urns {
		if moving[u] {
			out = append(out, m.dest.rehome(u))
		}
	}
	return out
}

// lazyDecrypter opens side's secrets manager from its secrets record the first time a secret is decrypted. Most
// moves carry no secret and never open it.
func (m *mover) lazyDecrypter(ctx context.Context, side moveSide) config.Decrypter {
	return m.lazyCrypter(ctx, side)
}

func (m *mover) lazyCrypter(ctx context.Context, side moveSide) *lazyCrypter {
	return &lazyCrypter{open: func() (secrets.Manager, error) {
		key := side.keys.secrets()
		payload, _, exists, err := m.store.Get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("plunge: reading the record at %q: %w", key, err)
		}
		if !exists {
			return nil, fmt.Errorf("plunge: a moved record holds a secret, and %s has no secrets record at %q; run "+
				"`plunge up` on it once so it has a secrets configuration", side.ref, key)
		}
		var s secretsRecord
		if err := decodeRecord(key, payload, secretsFormat, &s); err != nil {
			return nil, err
		}
		if m.opts.SecretsProvider == nil {
			return nil, errors.New("plunge: a moved record holds a secret, and no secrets provider was given")
		}
		sm, err := m.opts.SecretsProvider.OfType(ctx, s.Type, s.State)
		if err != nil {
			return nil, fmt.Errorf("plunge: opening the %s secrets manager of %s from %q: %w", s.Type, side.ref, key, err)
		}
		return sm, nil
	}}
}

// lazyCrypter is a stack's secrets manager, opened on first use.
type lazyCrypter struct {
	open func() (secrets.Manager, error)
	once sync.Once
	sm   secrets.Manager
	err  error
}

func (c *lazyCrypter) get() (secrets.Manager, error) {
	c.once.Do(func() { c.sm, c.err = c.open() })
	return c.sm, c.err
}

func (c *lazyCrypter) EncryptValue(ctx context.Context, plaintext string) (string, error) {
	sm, err := c.get()
	if err != nil {
		return "", err
	}
	return sm.Encrypter().EncryptValue(ctx, plaintext)
}

func (c *lazyCrypter) BatchEncrypt(ctx context.Context, plaintexts []string) ([]string, error) {
	sm, err := c.get()
	if err != nil {
		return nil, err
	}
	return sm.Encrypter().BatchEncrypt(ctx, plaintexts)
}

func (c *lazyCrypter) DecryptValue(ctx context.Context, ciphertext string) (string, error) {
	sm, err := c.get()
	if err != nil {
		return "", err
	}
	return sm.Decrypter().DecryptValue(ctx, ciphertext)
}

func (c *lazyCrypter) BatchDecrypt(ctx context.Context, ciphertexts []string) ([]string, error) {
	sm, err := c.get()
	if err != nil {
		return nil, err
	}
	return sm.Decrypter().BatchDecrypt(ctx, ciphertexts)
}

// rehomeMarkers returns props with every marker set in it (a tags or labels map, or a key/value tag list, at any
// depth) replaced by urn's markers in the encoding that set used, and the markers it wrote. marked is false when
// props carry no marker key; props are then returned as given.
func rehomeMarkers(
	props resource.PropertyMap, urn resource.URN,
) (out resource.PropertyMap, written map[string]string, marked bool, err error) {
	w := &markerRewriter{urn: urn}
	out = w.object(props)
	if w.err != nil {
		return nil, nil, false, w.err
	}
	if !w.found {
		return props, nil, false, nil
	}
	return out, w.written, true, nil
}

type markerRewriter struct {
	urn     resource.URN
	found   bool
	written map[string]string
	err     error
}

// want is urn's markers in the encoding stackValue (an old TagStack value) shows: readable for tags, digests for
// labels.
func (w *markerRewriter) want(stackValue string, has bool) map[string]string {
	enc := markers.EncodingTags
	if has && !strings.Contains(stackValue, "/") {
		enc = markers.EncodingLabels
	}
	want, err := markers.For(w.urn, enc)
	if err != nil {
		w.err = err
		return nil
	}
	w.found = true
	if w.written == nil || enc == markers.EncodingTags {
		w.written = want
	}
	return want
}

func (w *markerRewriter) value(v resource.PropertyValue) resource.PropertyValue {
	switch {
	case v.IsSecret():
		return resource.MakeSecret(w.value(v.SecretValue().Element))
	case v.IsOutput():
		o := v.OutputValue()
		if !o.Known {
			return v
		}
		o.Element = w.value(o.Element)
		return resource.NewProperty(o)
	case v.IsObject():
		return resource.NewProperty(w.object(v.ObjectValue()))
	case v.IsArray():
		return resource.NewProperty(w.array(v.ArrayValue()))
	}
	return v
}

func (w *markerRewriter) object(obj resource.PropertyMap) resource.PropertyMap {
	out := make(resource.PropertyMap, len(obj))
	hasMarker, stackValue, hasStack := false, "", false
	for k, e := range obj {
		if markers.IsMarkerKey(string(k)) {
			hasMarker = true
			if strings.EqualFold(string(k), markers.TagStack) {
				if s := unwrapValue(e); s.IsString() {
					stackValue, hasStack = s.StringValue(), true
				}
			}
			continue
		}
		out[k] = w.value(e)
	}
	if !hasMarker {
		return out
	}
	for k, v := range w.want(stackValue, hasStack) {
		out[resource.PropertyKey(k)] = resource.NewProperty(v)
	}
	return out
}

func (w *markerRewriter) array(arr []resource.PropertyValue) []resource.PropertyValue {
	out := make([]resource.PropertyValue, 0, len(arr))
	hasMarker, stackValue, hasStack := false, "", false
	for _, e := range arr {
		if k, v, ok := keyValueEntry(e); ok && markers.IsMarkerKey(k) {
			hasMarker = true
			if strings.EqualFold(k, markers.TagStack) {
				stackValue, hasStack = v, true
			}
			continue
		}
		out = append(out, w.value(e))
	}
	if !hasMarker {
		return out
	}
	want := w.want(stackValue, hasStack)
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, resource.NewProperty(resource.PropertyMap{
			"key":   resource.NewProperty(k),
			"value": resource.NewProperty(want[k]),
		}))
	}
	return out
}

// keyValueEntry reads a {key, value} tag list entry.
func keyValueEntry(e resource.PropertyValue) (string, string, bool) {
	e = unwrapValue(e)
	if !e.IsObject() {
		return "", "", false
	}
	k, v := unwrapValue(e.ObjectValue()["key"]), unwrapValue(e.ObjectValue()["value"])
	if !k.IsString() {
		return "", "", false
	}
	val := ""
	if v.IsString() {
		val = v.StringValue()
	}
	return k.StringValue(), val, true
}

// markerStackValues is every TagStack value props carry, at any depth, in a map or a key/value tag list.
func markerStackValues(props resource.PropertyMap) []string {
	var out []string
	var walk func(v resource.PropertyValue)
	walk = func(v resource.PropertyValue) {
		v = unwrapValue(v)
		switch {
		case v.IsObject():
			for k, e := range v.ObjectValue() {
				if strings.EqualFold(string(k), markers.TagStack) {
					if s := unwrapValue(e); s.IsString() {
						out = append(out, s.StringValue())
					}
					continue
				}
				walk(e)
			}
		case v.IsArray():
			for _, e := range v.ArrayValue() {
				if k, val, ok := keyValueEntry(e); ok && strings.EqualFold(k, markers.TagStack) {
					out = append(out, val)
					continue
				}
				walk(e)
			}
		}
	}
	walk(resource.NewProperty(props))
	sort.Strings(out)
	return out
}
