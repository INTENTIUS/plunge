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

// Per-resource drift (INTENTIUS/plunge#1). One resource's drift is read with one provider Read and no stack-wide
// refresh: the URN's record and its provider's record are read by key (and the secrets record, only when one of them
// holds a ciphertext), the provider is loaded and configured from its record, Read is called once, and the live
// outputs are compared with the recorded ones. The snapshot is never assembled, which the read counts show: assembly
// needs a listing of the stack's records, and this path makes none.
//
// What is compared: the outputs the record holds against the outputs Read returns, and the ID. The program's inputs
// are not consulted, because they exist only by running the program, which is the stack-wide work this path avoids.
// The inputs Read returns are not compared either: providers rebuild them from the live object, and a difference
// there is what the next plan shows, not drift.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/backend"
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
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/sig"
)

// DriftEngine and DriftRead are what every record this path writes names: the engine is plunge, and the read is a
// refresh of one resource (chant's DriftRead "refresh").
const (
	DriftEngine  = "plunge"
	DriftRead    = "refresh"
	DriftVersion = "v1"
)

// DriftRequest is one resource's drift read.
type DriftRequest struct {
	// URN is the resource to read.
	URN resource.URN
	// SecretsProvider opens the stack's secrets manager from its secrets record, when a record holds a secret.
	SecretsProvider secrets.Provider
	// Plugins is the plugin context the provider is loaded through.
	Plugins *plugin.Context
	// Member is chant's member name for the stack, the record's `member`.
	Member string
	// Version and Command go into each record's evidence.
	Version string
	Command string
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// BackendClient is what the builtin `pulumi` provider reads other stacks through (a StackReference's Read).
	// ReadResourceDrift sets it from the backend when nil.
	BackendClient deploy.BackendClient
}

// DriftResult is the record the read produced and what the read cost.
type DriftResult struct {
	Record DriftRecord
	Reads  DriftReads
}

// DriftRecord is chant's DriftRecord (chant packages/core/src/drift-record.ts), field for field.
type DriftRecord struct {
	Member       string           `json:"member"`
	Engine       string           `json:"engine"`
	Address      string           `json:"address"`
	ResourceType string           `json:"resourceType,omitempty"`
	Node         *string          `json:"node"`
	ObservedAt   string           `json:"observedAt"`
	Read         string           `json:"read"`
	Status       string           `json:"status"`
	Unobserved   *DriftUnobserved `json:"unobserved,omitempty"`
	Attributes   []DriftAttribute `json:"attributes,omitempty"`
	Evidence     DriftEvidence    `json:"evidence"`
}

// DriftUnobserved is DriftRecord.unobserved. Reason is one of chant's UnobservedReason values.
type DriftUnobserved struct {
	Reason  string `json:"reason"`
	Detail  string `json:"detail,omitempty"`
	Queried string `json:"queried,omitempty"`
}

// DriftAttribute is one attribute that differs. Before is the recorded value and After the live one. A sensitive
// attribute carries neither.
type DriftAttribute struct {
	Path      string `json:"path"`
	Before    any    `json:"before,omitempty"`
	After     any    `json:"after,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

// DriftEvidence is DriftRecord.evidence.
type DriftEvidence struct {
	Tool    string `json:"tool"`
	Version string `json:"version,omitempty"`
	Command string `json:"command,omitempty"`
}

// DriftReads is the settle line's `reads`: what one per-resource drift read cost. RecordsRead is every keyed read
// that reached the record store and RecordKeys how many distinct keys those were. RecordListCalls is every listing
// or bulk read of the store, which this path never makes: a non-zero value is a defect, and the read is refused as
// one. ProviderReads is the provider Read calls. StackRefreshes is the stack-wide refreshes run, and this path has
// none to run.
type DriftReads struct {
	RecordsRead     int `json:"recordsRead"`
	RecordKeys      int `json:"recordKeys"`
	RecordListCalls int `json:"recordListCalls"`
	ProviderReads   int `json:"providerReads"`
	StackRefreshes  int `json:"stackRefreshes"`
}

// NotPlungeBackendError refuses a per-resource drift read on a stack whose backend keeps no records per resource.
type NotPlungeBackendError struct {
	URL string
}

func (e *NotPlungeBackendError) Error() string {
	return fmt.Sprintf("plunge: a per-resource drift read needs the plunge backend (%sfile://, %ss3://, ...), "+
		"which keeps one record per resource; the current backend is %s", URLPrefix, URLPrefix, e.URL)
}

// NoRecordError refuses a drift read of a URN the stack holds no record for.
type NoRecordError struct {
	URN resource.URN
	Key string
}

func (e *NoRecordError) Error() string {
	return fmt.Sprintf("plunge: the stack holds no record for %s (looked at %q)", e.URN, e.Key)
}

// ListedError is the defect a drift read refuses: it listed the record store where it reads by key only.
type ListedError struct {
	Calls int
}

func (e *ListedError) Error() string {
	return fmt.Sprintf("plunge: this drift read listed the record store %d time(s), where it reads the resource's "+
		"records by key and lists nothing; the record above is still the read's answer, but the read was not the one "+
		"this path promises (INTENTIUS/plunge#1)", e.Calls)
}

// ReadResourceDrift reads one resource's drift on a stack of the plunge backend.
func ReadResourceDrift(
	ctx context.Context, be backend.Backend, ref backend.StackReference, req DriftRequest,
) (*DriftResult, error) {
	pb, ok := be.(*plungeBackend)
	if !ok {
		return nil, &NotPlungeBackendError{URL: be.URL()}
	}
	keys, err := keysFor(ref)
	if err != nil {
		return nil, err
	}
	if req.BackendClient == nil {
		req.BackendClient = backend.NewBackendClient(pb, req.SecretsProvider)
	}
	return resourceDrift(ctx, pb.store, keys, req)
}

// keyedStore counts what reaches the store: keyed reads, and any listing.
type keyedStore struct {
	staterecord.Store
	mu    sync.Mutex
	gets  int
	keys  map[string]bool
	lists int
}

func newKeyedStore(store staterecord.Store) *keyedStore {
	return &keyedStore{Store: store, keys: map[string]bool{}}
}

func (s *keyedStore) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	s.mu.Lock()
	s.gets++
	s.keys[key] = true
	s.mu.Unlock()
	return s.Store.Get(ctx, key)
}

func (s *keyedStore) List(ctx context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	s.lists++
	s.mu.Unlock()
	return s.Store.List(ctx, prefix)
}

func (s *keyedStore) reads() DriftReads {
	s.mu.Lock()
	defer s.mu.Unlock()
	return DriftReads{RecordsRead: s.gets, RecordKeys: len(s.keys), RecordListCalls: s.lists}
}

// readResourceRecord reads one URN's record by key. A missing record is NoRecordError.
func readResourceRecord(
	ctx context.Context, store staterecord.Store, keys stackKeys, urn resource.URN,
) (*resourceRecord, []byte, error) {
	key := keys.resource(urn)
	payload, _, exists, err := store.Get(ctx, key)
	if err != nil {
		return nil, nil, fmt.Errorf("plunge: reading the record at %q: %w", key, err)
	}
	if !exists {
		return nil, nil, &NoRecordError{URN: urn, Key: key}
	}
	var r resourceRecord
	if err := decodeRecord(key, payload, resourceFormat, &r); err != nil {
		return nil, nil, err
	}
	if r.URN != urn {
		return nil, nil, fmt.Errorf("plunge: the record at %q says it is for %s, but its key names %s; "+
			"refusing to read one resource's state from another's record", key, r.URN, urn)
	}
	return &r, payload, nil
}

// liveState is the record's live state: the one that is not a pending delete.
func liveState(r *resourceRecord) (apitype.ResourceV3, bool) {
	for _, s := range r.States {
		if !s.Delete {
			return s, true
		}
	}
	return apitype.ResourceV3{}, false
}

// providerState is the state of the provider ref names, from the provider URN's record.
func providerState(r *resourceRecord, ref providers.Reference) (apitype.ResourceV3, bool) {
	for _, s := range r.States {
		if s.ID == ref.ID() {
			return s, true
		}
	}
	return apitype.ResourceV3{}, false
}

// holdsSecret reports whether a record payload carries a secret envelope, which is when the secrets record must be
// read to decrypt it.
func holdsSecret(payload []byte) bool {
	return bytes.Contains(payload, []byte(sig.Secret))
}

// resourceDrift is the drift read on a store: the records by key, the provider configured from its record, one Read,
// and the record.
func resourceDrift(ctx context.Context, store staterecord.Store, keys stackKeys, req DriftRequest) (*DriftResult, error) {
	counted := newKeyedStore(store)
	now := time.Now
	if req.Now != nil {
		now = req.Now
	}

	res, resPayload, err := readResourceRecord(ctx, counted, keys, req.URN)
	if err != nil {
		return nil, err
	}

	rec := DriftRecord{
		Member:   req.Member,
		Engine:   DriftEngine,
		Address:  string(req.URN),
		Read:     DriftRead,
		Evidence: DriftEvidence{Tool: "plunge", Version: req.Version, Command: req.Command},
	}
	finish := func(reads int) (*DriftResult, error) {
		rec.ObservedAt = now().UTC().Format(time.RFC3339Nano)
		r := counted.reads()
		r.ProviderReads = reads
		out := &DriftResult{Record: rec, Reads: r}
		if r.RecordListCalls > 0 {
			return out, &ListedError{Calls: r.RecordListCalls}
		}
		return out, nil
	}
	unobserved := func(reason, detail string, reads int) (*DriftResult, error) {
		rec.Status = "not-observed"
		rec.Unobserved = &DriftUnobserved{Reason: reason, Detail: detail}
		return finish(reads)
	}

	state, ok := liveState(res)
	if !ok {
		return unobserved("not-reached", "the record holds only states pending delete, so no live resource is recorded", 0)
	}
	rec.ResourceType = string(state.Type)
	switch {
	case !state.Custom:
		return unobserved("unsupported-kind", "a component resource has no provider Read; it has no state of its own to drift", 0)
	case providers.IsProviderType(state.Type):
		return unobserved("unsupported-kind", "a provider resource is configuration, not a cloud object, and has no Read", 0)
	case state.ID == "":
		return unobserved("not-reached", "the record has no ID for the resource, so there is nothing to read", 0)
	case state.Provider == "":
		return unobserved("no-binding", "the record names no provider for the resource", 0)
	}

	ref, err := providers.ParseReference(state.Provider)
	if err != nil {
		return nil, fmt.Errorf("plunge: the record for %s names the provider %q, which is not a provider reference: %w",
			req.URN, state.Provider, err)
	}
	provRec, provPayload, err := readResourceRecord(ctx, counted, keys, ref.URN())
	if err != nil {
		return nil, err
	}
	provV3, ok := providerState(provRec, ref)
	if !ok {
		return nil, fmt.Errorf("plunge: the record for the provider %s holds no state with ID %s, which %s names",
			ref.URN(), ref.ID(), req.URN)
	}

	// The resource's views, each from its own record by key, for Read's old views (a refresh step passes them too).
	var viewStates []apitype.ResourceV3
	viewSecrets := false
	for _, viewURN := range res.Views {
		viewRec, viewPayload, err := readResourceRecord(ctx, counted, keys, viewURN)
		if err != nil {
			var none *NoRecordError
			if errors.As(err, &none) {
				continue // the view was deleted since the record listed it
			}
			return nil, err
		}
		if v, ok := liveState(viewRec); ok && v.ViewOf == req.URN {
			viewStates = append(viewStates, v)
			viewSecrets = viewSecrets || holdsSecret(viewPayload)
		}
	}

	// The secrets record is read only when a record holds a ciphertext: most resources carry none.
	var dec config.Decrypter = config.NewPanicCrypter()
	if holdsSecret(resPayload) || holdsSecret(provPayload) || viewSecrets {
		if dec, err = recordDecrypter(ctx, counted, keys, req.SecretsProvider); err != nil {
			return nil, err
		}
	}
	st, err := stack.DeserializeResource(state, dec)
	if err != nil {
		return nil, fmt.Errorf("plunge: decoding the recorded state of %s: %w", req.URN, err)
	}
	prov, err := stack.DeserializeResource(provV3, dec)
	if err != nil {
		return nil, fmt.Errorf("plunge: decoding the recorded state of the provider %s: %w", ref.URN(), err)
	}

	oldViews := make([]plugin.View, 0, len(viewStates))
	for _, v := range viewStates {
		vs, err := stack.DeserializeResource(v, dec)
		if err != nil {
			return nil, fmt.Errorf("plunge: decoding the recorded state of the view %s: %w", v.URN, err)
		}
		view := plugin.View{
			Type:    vs.URN.Type(),
			Name:    vs.URN.Name(),
			Inputs:  resource.FromResourcePropertyMap(vs.Inputs),
			Outputs: resource.FromResourcePropertyMap(vs.Outputs),
		}
		if vs.Parent != "" && vs.Parent != vs.ViewOf {
			view.ParentType = vs.Parent.Type()
			view.ParentName = vs.Parent.Name()
		}
		oldViews = append(oldViews, view)
	}

	if req.Plugins == nil {
		return nil, errors.New("plunge: a drift read needs a plugin context to load the provider")
	}
	// The builtin provider serves the `pulumi` package (StackReference and the rest), as in a deployment.
	reg := deployproviders.NewRegistry(req.Plugins, false, deploy.NewBuiltinProvider(req.BackendClient, req.Plugins.Diag))
	defer func() { _ = reg.Close() }()
	if err := reg.Same(ctx, prov, false); err != nil {
		return unobserved("read-failed", fmt.Sprintf("loading and configuring the provider %s: %v", ref.URN(), err), 0)
	}
	p, ok := reg.GetProvider(ref)
	if !ok {
		return unobserved("read-failed", fmt.Sprintf("the provider %s was configured and is not registered", ref), 0)
	}

	status, err := startDriftStatusServer()
	if err != nil {
		return nil, err
	}
	defer status.close()
	resp, err := p.Read(ctx, plugin.ReadRequest{
		URN:                   st.URN,
		Name:                  st.URN.Name(),
		Type:                  st.URN.Type(),
		ID:                    st.ID,
		Inputs:                resource.FromResourcePropertyMap(st.Inputs),
		State:                 resource.FromResourcePropertyMap(st.Outputs),
		Timeout:               st.CustomTimeouts.Read,
		ResourceStatusAddress: status.address(),
		ResourceStatusToken:   status.token,
		OldViews:              oldViews,
	})
	if err != nil && resp.Status != resource.StatusPartialFailure {
		return unobserved("read-failed", err.Error(), 1)
	}
	if resp.ID == "" || resp.Outputs == nil {
		rec.Status = "missing"
		return finish(1)
	}

	attrs := outputDrift(st, resp.ID, resource.ToResourcePropertyMap(*resp.Outputs))
	if len(attrs) == 0 {
		rec.Status = "in-sync"
	} else {
		rec.Status = "drifted"
		rec.Attributes = attrs
	}
	return finish(1)
}

// recordDecrypter opens the stack's secrets manager from its secrets record.
func recordDecrypter(
	ctx context.Context, store staterecord.Store, keys stackKeys, sp secrets.Provider,
) (config.Decrypter, error) {
	key := keys.secrets()
	payload, _, exists, err := store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("plunge: reading the record at %q: %w", key, err)
	}
	if !exists {
		return nil, fmt.Errorf("plunge: a record holds a secret, and the stack has no secrets record at %q", key)
	}
	var s secretsRecord
	if err := decodeRecord(key, payload, secretsFormat, &s); err != nil {
		return nil, err
	}
	if sp == nil {
		return nil, errors.New("plunge: a record holds a secret, and no secrets provider was given to decrypt it")
	}
	sm, err := sp.OfType(ctx, s.Type, s.State)
	if err != nil {
		return nil, fmt.Errorf("plunge: opening the %s secrets manager from %q: %w", s.Type, key, err)
	}
	return sm.Decrypter(), nil
}

// outputDrift is every attribute where the live outputs differ from the recorded ones, plus the ID when it moved.
// Secretness is not a difference: values are compared with their secret wrappers taken off. An attribute whose
// recorded or live value holds a secret, or that the resource marks as an additional secret output, is sensitive
// and carries no values. Keys starting with "__" are provider bookkeeping and left out, as are null values on one
// side only, which a provider reports as absent or as null at random.
func outputDrift(st *pkgresource.State, liveID resource.ID, live resource.PropertyMap) []DriftAttribute {
	var out []DriftAttribute
	if liveID != st.ID {
		out = append(out, DriftAttribute{Path: "id", Before: string(st.ID), After: string(liveID)})
	}

	secretKeys := map[resource.PropertyKey]bool{}
	for _, k := range st.AdditionalSecretOutputs {
		secretKeys[k] = true
	}
	for k, v := range st.Outputs {
		if v.ContainsSecrets() {
			secretKeys[k] = true
		}
	}
	for k, v := range live {
		if v.ContainsSecrets() {
			secretKeys[k] = true
		}
	}

	internal := func(k resource.PropertyKey) bool { return strings.HasPrefix(string(k), "__") }
	diff := unsecretMap(st.Outputs).Diff(unsecretMap(live), internal)
	if diff == nil {
		return out
	}
	for _, a := range objectDrift("", diff) {
		top := a.top
		if secretKeys[top] {
			out = append(out, DriftAttribute{Path: a.attr.Path, Sensitive: true})
			continue
		}
		out = append(out, a.attr)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

type flatAttribute struct {
	top  resource.PropertyKey
	attr DriftAttribute
}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// keyPath appends key to path in chant's attribute path grammar: keys joined with ".", and a key that is not an
// identifier in brackets and quotes.
func keyPath(path string, key resource.PropertyKey) string {
	k := string(key)
	if identifier.MatchString(k) {
		if path == "" {
			return k
		}
		return path + "." + k
	}
	quoted, _ := json.Marshal(k)
	return path + "[" + string(quoted) + "]"
}

func indexPath(path string, i int) string { return fmt.Sprintf("%s[%d]", path, i) }

// objectDrift flattens an object diff into one attribute per differing leaf.
func objectDrift(path string, d *resource.ObjectDiff) []flatAttribute {
	var out []flatAttribute
	topOf := func(k resource.PropertyKey) resource.PropertyKey {
		if path == "" {
			return k
		}
		return ""
	}
	for k, v := range d.Adds {
		if v.IsNull() {
			continue
		}
		out = append(out, flatAttribute{topOf(k), DriftAttribute{Path: keyPath(path, k), After: v.Mappable()}})
	}
	for k, v := range d.Deletes {
		if v.IsNull() {
			continue
		}
		out = append(out, flatAttribute{topOf(k), DriftAttribute{Path: keyPath(path, k), Before: v.Mappable()}})
	}
	for k, vd := range d.Updates {
		for _, a := range valueDrift(keyPath(path, k), vd) {
			if path == "" {
				a.top = k
			}
			out = append(out, a)
		}
	}
	return out
}

func valueDrift(path string, vd resource.ValueDiff) []flatAttribute {
	switch {
	case vd.Object != nil:
		return objectDrift(path, vd.Object)
	case vd.Array != nil:
		var out []flatAttribute
		for i, v := range vd.Array.Adds {
			out = append(out, flatAttribute{attr: DriftAttribute{Path: indexPath(path, i), After: v.Mappable()}})
		}
		for i, v := range vd.Array.Deletes {
			out = append(out, flatAttribute{attr: DriftAttribute{Path: indexPath(path, i), Before: v.Mappable()}})
		}
		for i, e := range vd.Array.Updates {
			out = append(out, valueDrift(indexPath(path, i), e)...)
		}
		return out
	default:
		return []flatAttribute{{attr: DriftAttribute{Path: path, Before: vd.Old.Mappable(), After: vd.New.Mappable()}}}
	}
}

// unsecretMap is m with every secret and output wrapper taken off, so a value that is the same up to secretness
// compares equal.
func unsecretMap(m resource.PropertyMap) resource.PropertyMap {
	out := make(resource.PropertyMap, len(m))
	for k, v := range m {
		out[k] = unsecret(v)
	}
	return out
}

func unsecret(v resource.PropertyValue) resource.PropertyValue {
	switch {
	case v.IsSecret():
		return unsecret(v.SecretValue().Element)
	case v.IsOutput():
		return unsecret(v.OutputValue().Element)
	case v.IsObject():
		return resource.NewProperty(unsecretMap(v.ObjectValue()))
	case v.IsArray():
		arr := v.ArrayValue()
		out := make([]resource.PropertyValue, len(arr))
		for i, e := range arr {
			out[i] = unsecret(e)
		}
		return resource.NewProperty(out)
	default:
		return v
	}
}
