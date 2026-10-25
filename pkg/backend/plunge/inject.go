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

// The marker injector (INTENTIUS/plunge#7): deploy.MarkerPolicy for the plunge backend. The engine calls it after
// every transform has run (pkg/resource/deploy/plunge_markers.go), so the markers land in the inputs the provider
// checks, and stock `pulumi preview` after a back-out shows their removal and nothing else.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	pkgresource "github.com/pulumi/pulumi/pkg/v3/resource"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	sdkproviders "github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// The autonaming policy, which used to sit here as an undecided seam, is autoname.go: deploy.AutonamePolicy,
// implemented by autonamePolicy (option A of INTENTIUS/plunge#7, ruled 2026-10-25).

// Ownership is how plunge knows a resource belongs to a stack.
type Ownership string

const (
	// OwnershipMarker is a resource whose live object carries the stack's markers.
	OwnershipMarker Ownership = "marker"
	// OwnershipRecord is a resource whose ownership is carried by its record alone: an untaggable type, as
	// choudoufu's markerless types are.
	OwnershipRecord Ownership = "record"
)

// MarkerState is what the injector did for one resource in one run.
type MarkerState string

const (
	MarkerInjected     MarkerState = "injected"
	MarkerTagsUnknown  MarkerState = "tags-unknown-at-preview"
	MarkerUntaggable   MarkerState = "untaggable"
	MarkerUnsupported  MarkerState = "unsupported-shape"
	MarkerSchemaAbsent MarkerState = "schema-unknown"
)

// ownershipEntry is what a resource record says about ownership. It rides in the URN's record
// (resourceRecord.Ownership), so record-carried ownership needs no other store.
type ownershipEntry struct {
	By     Ownership         `json:"by"`
	State  MarkerState       `json:"state"`
	Family Family            `json:"family,omitempty"`
	TFType string            `json:"tfType,omitempty"`
	Marker map[string]string `json:"markers,omitempty"`
	Reason string            `json:"reason,omitempty"`
	// Identity is pkg/identity's claim about what names the type, for an operator re-importing after a back-out.
	Identity string `json:"identity,omitempty"`
	// Bound is the live ID a create was bound to by its markers in the run that wrote the record.
	Bound resource.ID `json:"bound,omitempty"`
	// Unlisted says why the type had no consistent lister when it was created, so no bind or collision check ran.
	Unlisted string `json:"unlisted,omitempty"`
	// Autoname is where the resource's name came from and whether C1 covers it (autoname.go).
	Autoname *autonameEntry `json:"autoname,omitempty"`
}

// ledger is one run's ownership entries, by URN. The persister writes them into the resource records.
type ledger struct {
	mu      sync.Mutex
	entries map[resource.URN]*ownershipEntry
}

func newLedger() *ledger { return &ledger{entries: map[resource.URN]*ownershipEntry{}} }

func (l *ledger) entry(urn resource.URN) *ownershipEntry {
	e, ok := l.entries[urn]
	if !ok {
		e = &ownershipEntry{}
		l.entries[urn] = e
	}
	return e
}

func (l *ledger) record(urn resource.URN, fn func(e *ownershipEntry)) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fn(l.entry(urn))
}

func (l *ledger) get(urn resource.URN) (ownershipEntry, bool) {
	if l == nil {
		return ownershipEntry{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[urn]
	if !ok {
		return ownershipEntry{}, false
	}
	return *e, true
}

// CreateOnlyTagsError refuses adding a marker to an existing resource whose tags are create-only: the provider
// would replace it. plunge never replaces a resource to mark it.
type CreateOnlyTagsError struct {
	URN      resource.URN
	Type     tokens.Type
	Property string
}

func (e *CreateOnlyTagsError) Error() string {
	return fmt.Sprintf("plunge: refusing to mark %s: %s's %q can only be set when the resource is created, so adding "+
		"the ownership marker would replace the live resource, and plunge never replaces a resource to mark it. "+
		"Leave this stack on stock Pulumi, or remove the resource from the stack before adopting", e.URN, e.Type,
		e.Property)
}

// HiddenMarkersError refuses a provider configured to drop or override plunge's marker keys.
type HiddenMarkersError struct {
	Provider resource.URN
	Setting  string
	Value    string
}

func (e *HiddenMarkersError) Error() string {
	return fmt.Sprintf("plunge: refusing the provider %s: its %s setting %q matches the marker prefix %q, so the "+
		"ownership markers would be dropped or overwritten without a word. Remove that entry (plunge's markers are "+
		"%s and %s)", e.Provider, e.Setting, e.Value, markers.Prefix, markers.TagStack, markers.TagURN)
}

// markerPolicy is the plunge backend's deploy.MarkerPolicy.
type markerPolicy struct {
	catalog *catalog
	base    map[resource.URN]*pkgresource.State
	ledger  *ledger
	diag    diag.Sink
}

var _ deploy.MarkerPolicy = (*markerPolicy)(nil)

func newMarkerPolicy(c *catalog, snap *deploy.Snapshot, l *ledger, d diag.Sink) *markerPolicy {
	base := map[resource.URN]*pkgresource.State{}
	if snap != nil {
		for _, s := range snap.Resources {
			if !s.Delete {
				base[s.URN] = s
			}
		}
	}
	return &markerPolicy{catalog: c, base: base, ledger: l, diag: d}
}

func (p *markerPolicy) warn(urn resource.URN, format string, args ...any) {
	if p.diag != nil {
		p.diag.Warningf(diag.Message(urn, format), args...)
	}
}

// Inject implements deploy.MarkerPolicy.
func (p *markerPolicy) Inject(ctx context.Context, req deploy.MarkerRequest) (resource.PropertyMap, error) {
	info := p.catalog.lookup(ctx, req.Provider, req.Type)
	note := func(state MarkerState, by Ownership, reason string) {
		p.ledger.record(req.URN, func(e *ownershipEntry) {
			e.By, e.State, e.Family, e.TFType, e.Reason = by, state, info.Family, info.TFType, reason
			if info.Identity != nil {
				e.Identity = info.Identity.Claim()
			}
		})
	}
	if info.Family == FamilyAzureNative {
		if _, ok := req.Properties[resource.PropertyKey(info.Property)]; ok {
			info.Shape, info.Reason = ShapeMap, ""
		}
	}
	switch info.Shape {
	case ShapeNone:
		note(MarkerUntaggable, OwnershipRecord, info.Reason)
		return req.Properties, nil
	case ShapeUnsupported:
		note(MarkerUnsupported, OwnershipRecord, info.Reason)
		p.warn(req.URN, "plunge: no ownership marker for this resource: %s; its record carries ownership", info.Reason)
		return req.Properties, nil
	case ShapeUnknown:
		note(MarkerSchemaAbsent, OwnershipRecord, info.Reason)
		return req.Properties, nil
	}
	if info.Markerless {
		note(MarkerUntaggable, OwnershipRecord, "pkg/identity classes "+info.TFType+" as markerless")
		return req.Properties, nil
	}

	want, err := markers.For(req.URN, info.Encoding())
	if err != nil {
		return nil, err
	}
	if old, ok := p.base[req.URN]; ok && info.CreateOnly && !carries(old.Inputs, info, want) {
		return nil, &CreateOnlyTagsError{URN: req.URN, Type: req.Type, Property: info.Property}
	}
	out, ok := mergeMarkers(req.Properties, info, want)
	if !ok {
		// Unknown tags during preview: the program's tags depend on an output not known yet, so the markers cannot
		// be merged into them. They are merged at `up`, when the value is known; the preview says so.
		note(MarkerTagsUnknown, OwnershipMarker, fmt.Sprintf("%s is not known during preview", info.Property))
		if req.DryRun {
			p.warn(req.URN, "plunge: %s is not known yet, so the preview does not show the ownership markers; "+
				"`up` adds them once it is known", info.Property)
			return req.Properties, nil
		}
		return nil, fmt.Errorf("plunge: %s's %s is not known at update time, so its ownership markers cannot be "+
			"added", req.URN, info.Property)
	}
	p.ledger.record(req.URN, func(e *ownershipEntry) {
		e.By, e.State, e.Family, e.TFType, e.Marker, e.Reason = OwnershipMarker, MarkerInjected, info.Family,
			info.TFType, want, ""
		if info.Identity != nil {
			e.Identity = info.Identity.Claim()
		}
	})
	return out, nil
}

// carries reports whether inputs already hold every marker in want, where info says they go.
func carries(inputs resource.PropertyMap, info TypeInfo, want map[string]string) bool {
	return markers.Carries(tagsOf(inputs, info), want)
}

// tagsOf reads the tags a resource's inputs hold where info says markers go, ignoring unknowns.
func tagsOf(inputs resource.PropertyMap, info TypeInfo) map[string]string {
	out := map[string]string{}
	v, ok := inputs[resource.PropertyKey(info.Property)]
	if !ok {
		return out
	}
	v = unwrapValue(v)
	if info.Family == FamilyKubernetes {
		if !v.IsObject() {
			return out
		}
		v = unwrapValue(v.ObjectValue()["labels"])
	}
	switch {
	case info.Shape == ShapeKeyValueArray && v.IsArray():
		for _, e := range v.ArrayValue() {
			e = unwrapValue(e)
			if !e.IsObject() {
				continue
			}
			k, val := unwrapValue(e.ObjectValue()["key"]), unwrapValue(e.ObjectValue()["value"])
			if k.IsString() && val.IsString() {
				out[k.StringValue()] = val.StringValue()
			}
		}
	case v.IsObject():
		for k, e := range v.ObjectValue() {
			if e = unwrapValue(e); e.IsString() {
				out[string(k)] = e.StringValue()
			}
		}
	}
	return out
}

func unwrapValue(v resource.PropertyValue) resource.PropertyValue {
	for {
		switch {
		case v.IsSecret():
			v = v.SecretValue().Element
		case v.IsOutput() && v.OutputValue().Known:
			v = v.OutputValue().Element
		default:
			return v
		}
	}
}

// mergeMarkers returns props with want merged in at info's place. ok is false when the value there is unknown.
func mergeMarkers(props resource.PropertyMap, info TypeInfo, want map[string]string) (resource.PropertyMap, bool) {
	out := props.Copy()
	key := resource.PropertyKey(info.Property)
	if info.Family == FamilyKubernetes {
		meta, ok := rewrap(out[key], func(v resource.PropertyValue) (resource.PropertyValue, bool) {
			obj := resource.PropertyMap{}
			if v.IsObject() {
				obj = v.ObjectValue().Copy()
			} else if !v.IsNull() {
				return v, false
			}
			labels, ok := rewrap(obj["labels"], func(l resource.PropertyValue) (resource.PropertyValue, bool) {
				return mergeMap(l, want)
			})
			if !ok {
				return v, false
			}
			obj["labels"] = labels
			return resource.NewProperty(obj), true
		})
		if !ok {
			return props, false
		}
		out[key] = meta
		return out, true
	}
	merged, ok := rewrap(out[key], func(v resource.PropertyValue) (resource.PropertyValue, bool) {
		if info.Shape == ShapeKeyValueArray {
			return mergeKeyValueArray(v, want)
		}
		return mergeMap(v, want)
	})
	if !ok {
		return props, false
	}
	out[key] = merged
	return out, true
}

// rewrap applies fn inside any secret or known-output wrapper around v, keeping the wrapper (and an output's
// dependencies). An absent value is null. A computed value, or an unknown output, is not mergeable.
func rewrap(
	v resource.PropertyValue, fn func(resource.PropertyValue) (resource.PropertyValue, bool),
) (resource.PropertyValue, bool) {
	switch {
	case v.IsComputed():
		return v, false
	case v.IsSecret():
		inner, ok := rewrap(v.SecretValue().Element, fn)
		if !ok {
			return v, false
		}
		return resource.MakeSecret(inner), true
	case v.IsOutput():
		o := v.OutputValue()
		if !o.Known {
			return v, false
		}
		inner, ok := rewrap(o.Element, fn)
		if !ok {
			return v, false
		}
		o.Element = inner
		return resource.NewProperty(o), true
	}
	return fn(v)
}

func mergeMap(v resource.PropertyValue, want map[string]string) (resource.PropertyValue, bool) {
	obj := resource.PropertyMap{}
	switch {
	case v.IsObject():
		for k, e := range v.ObjectValue() {
			if !markers.IsMarkerKey(string(k)) {
				obj[k] = e
			}
		}
	case !v.IsNull():
		return v, false
	}
	for k, val := range want {
		obj[resource.PropertyKey(k)] = resource.NewProperty(val)
	}
	return resource.NewProperty(obj), true
}

func mergeKeyValueArray(v resource.PropertyValue, want map[string]string) (resource.PropertyValue, bool) {
	var arr []resource.PropertyValue
	switch {
	case v.IsArray():
		for _, e := range v.ArrayValue() {
			if inner := unwrapValue(e); inner.IsObject() {
				if k := unwrapValue(inner.ObjectValue()["key"]); k.IsString() && markers.IsMarkerKey(k.StringValue()) {
					continue
				}
			}
			arr = append(arr, e)
		}
	case !v.IsNull():
		return v, false
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		arr = append(arr, resource.NewProperty(resource.PropertyMap{
			"key":   resource.NewProperty(k),
			"value": resource.NewProperty(want[k]),
		}))
	}
	return resource.NewProperty(arr), true
}

// CheckProvider implements deploy.MarkerPolicy: it refuses, by name, a provider configuration that would drop
// markers (pulumi-aws's ignoreTags keys or keyPrefixes, pulumi-gcp's equivalent ignore lists) or override them
// (a defaultTags or defaultLabels entry with a marker key).
func (p *markerPolicy) CheckProvider(ctx context.Context, urn resource.URN, inputs resource.PropertyMap) error {
	return checkProviderInputs(urn, inputs)
}

func checkProviderInputs(urn resource.URN, inputs resource.PropertyMap) error {
	if !sdkproviders.IsProviderType(urn.Type()) {
		return nil
	}
	for _, setting := range []string{"ignoreTags", "ignoreLabels"} {
		v, ok := configValue(inputs, setting)
		if !ok {
			continue
		}
		obj, _ := v.(map[string]any)
		for _, field := range []struct {
			name   string
			prefix bool
		}{{"keys", false}, {"keyPrefixes", true}, {"key_prefixes", true}} {
			for _, k := range stringsOf(obj[field.name]) {
				if markers.HidesMarkers(k, field.prefix) {
					return &HiddenMarkersError{Provider: urn, Setting: setting + "." + field.name, Value: k}
				}
			}
		}
		// A bare list is a list of keys.
		for _, k := range stringsOf(v) {
			if markers.HidesMarkers(k, false) {
				return &HiddenMarkersError{Provider: urn, Setting: setting, Value: k}
			}
		}
	}
	for _, setting := range []string{"defaultTags", "defaultLabels"} {
		v, ok := configValue(inputs, setting)
		if !ok {
			continue
		}
		obj, _ := v.(map[string]any)
		if inner, ok := obj["tags"].(map[string]any); ok {
			obj = inner
		}
		for k := range obj {
			if markers.IsMarkerKey(k) {
				return &HiddenMarkersError{Provider: urn, Setting: setting, Value: k}
			}
		}
	}
	return nil
}

// configValue reads a provider input as plain data. Provider inputs carry object-typed config either as an object
// or as its JSON encoding in a string, depending on the SDK; both are read.
func configValue(inputs resource.PropertyMap, key string) (any, bool) {
	v, ok := inputs[resource.PropertyKey(key)]
	if !ok {
		return nil, false
	}
	v = unwrapValue(v)
	if v.IsNull() || v.IsComputed() {
		return nil, false
	}
	if v.IsString() {
		var out any
		if err := json.Unmarshal([]byte(v.StringValue()), &out); err == nil {
			return out, true
		}
		return v.StringValue(), true
	}
	return v.Mappable(), true
}

func stringsOf(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	}
	return nil
}

// mergeOwnership is the ownership a resource record should hold after this run: the ledger's entry when the run
// made one, over what the record held. A bind or a missing lister is a fact about the run that created the
// resource, so it stays on the record after later runs that neither bind nor list.
func mergeOwnership(prior *ownershipEntry, l *ledger, urn resource.URN) *ownershipEntry {
	e, ok := l.get(urn)
	if !ok {
		return prior
	}
	if prior != nil {
		if e.Bound == "" {
			e.Bound = prior.Bound
		}
		if e.Unlisted == "" {
			e.Unlisted = prior.Unlisted
		}
		if e.Autoname == nil {
			e.Autoname = prior.Autoname
		}
	}
	return &e
}
