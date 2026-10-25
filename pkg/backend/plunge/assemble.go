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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/version"
)

// known is one record as this run last read or wrote it: the version the store gave it, and its decoded payload.
// The persister compares against it to decide whether a record changed, and conditions every write on its version.
type known struct {
	version   string
	resource  *resourceRecord
	output    *outputRecord
	secrets   *secretsRecord
	snippet   *snippetRecord
	extension *extensionRecord
}

// generation is the stamp of the program that last wrote the record, or the zero stamp for records that carry none.
func (k *known) generation() Generation {
	switch {
	case k.resource != nil:
		return k.resource.Generation
	case k.output != nil:
		return k.output.Generation
	case k.snippet != nil:
		return k.snippet.Generation
	}
	return Generation{}
}

// loaded is a stack's records, read in one pass, and the deployment assembled from them.
type loaded struct {
	keys       stackKeys
	records    map[string]*known
	deployment apitype.TypedDeployment
	// generations is the stamp on each URN's record, for the delete guard.
	generations map[resource.URN]Generation
}

// MissingReferenceError refuses a snapshot assembled from records in which a resource refers to a parent, a
// dependency or a provider that no record holds. It names every such reference, so the operator can see which
// record is missing instead of meeting a snapshot integrity failure later.
type MissingReferenceError struct {
	Missing []MissingReference
}

// MissingReference is one dangling reference: the resource that holds it, what kind of reference it is, and the
// URN (or provider reference) it names.
type MissingReference struct {
	URN       resource.URN
	Kind      string
	Reference string
	Key       string
}

func (e *MissingReferenceError) Error() string {
	var b strings.Builder
	b.WriteString("plunge: refusing to assemble the stack's snapshot from its records: ")
	for i, m := range e.Missing {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "resource %s (record %s) refers to %s %s, and no record holds it", m.URN, m.Key, m.Kind, m.Reference)
	}
	return b.String()
}

// CycleError refuses records whose references cannot be put in an order where everything comes after what it
// refers to.
type CycleError struct {
	URNs []resource.URN
}

func (e *CycleError) Error() string {
	urns := make([]string, len(e.URNs))
	for i, u := range e.URNs {
		urns[i] = string(u)
	}
	return "plunge: refusing to assemble the stack's snapshot from its records: these resources refer to each other " +
		"in a cycle, so no order puts each after what it refers to: " + strings.Join(urns, ", ")
}

// readAll reads every record under the stack's namespace. A store that can enumerate values in bulk does it in one
// pass; any other store is a listing and one read per key.
func readAll(ctx context.Context, store staterecord.Store, keys stackKeys) (map[string]staterecord.Record, error) {
	if bulk, ok := store.(staterecord.BulkReader); ok {
		return bulk.GetAll(ctx, keys.prefix())
	}
	names, err := store.List(ctx, keys.prefix())
	if err != nil {
		return nil, err
	}
	out := make(map[string]staterecord.Record, len(names))
	for _, key := range names {
		payload, version, exists, err := store.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		if !exists {
			// Deleted between the listing and the read: another run removed it, which is the store's answer.
			continue
		}
		out[key] = staterecord.Record{Payload: payload, Version: version}
	}
	return out, nil
}

// load reads a stack's records and assembles them into a deployment.
func load(ctx context.Context, store staterecord.Store, keys stackKeys) (*loaded, error) {
	raw, err := readAll(ctx, store, keys)
	if err != nil {
		return nil, fmt.Errorf("plunge: reading the records under %q: %w", keys.prefix(), err)
	}
	return assemble(keys, raw, time.Now())
}

// assemble decodes records and builds the deployment they describe: resources in an order where each comes after
// its parent, its provider and everything it depends on; pending operations and the root Stack's outputs merged
// back in. A dangling reference is refused by name (MissingReferenceError), never left for VerifyIntegrity.
func assemble(keys stackKeys, raw map[string]staterecord.Record, now time.Time) (*loaded, error) {
	l := &loaded{keys: keys, records: map[string]*known{}, generations: map[resource.URN]Generation{}}

	sortedKeys := make([]string, 0, len(raw))
	for k := range raw {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var resources []*resourceRecord
	resourceKeys := map[resource.URN]string{}
	outputs := map[string]any{}
	var secrets *apitype.SecretsProvidersV1
	var snippets []apitype.SnippetV1
	extensions := map[apitype.ExtensionRef]apitype.Extension{}
	features := map[string]bool{}

	for _, key := range sortedKeys {
		rec := raw[key]
		k := &known{version: rec.Version}
		switch {
		case key == keys.secrets():
			var s secretsRecord
			if err := decodeRecord(key, rec.Payload, secretsFormat, &s); err != nil {
				return nil, err
			}
			k.secrets = &s
			secrets = &apitype.SecretsProvidersV1{Type: s.Type, State: s.State}
		case strings.HasPrefix(key, keys.resourcePrefix()):
			urn, ok := decodeSegment(key, keys.resourcePrefix())
			if !ok {
				return nil, fmt.Errorf("plunge: the record key %q is not a resource key this version writes", key)
			}
			var r resourceRecord
			if err := decodeRecord(key, rec.Payload, resourceFormat, &r); err != nil {
				return nil, err
			}
			if string(r.URN) != urn {
				return nil, fmt.Errorf("plunge: the record at %q says it is for %s, but its key names %s; "+
					"refusing to read one resource's state from another's record", key, r.URN, urn)
			}
			k.resource = &r
			resources = append(resources, &r)
			resourceKeys[r.URN] = key
			l.generations[r.URN] = r.Generation
			for _, f := range r.Features {
				features[f] = true
			}
		case strings.HasPrefix(key, keys.outputPrefix()):
			name, ok := decodeSegment(key, keys.outputPrefix())
			if !ok {
				return nil, fmt.Errorf("plunge: the record key %q is not an output key this version writes", key)
			}
			var o outputRecord
			if err := decodeRecord(key, rec.Payload, outputFormat, &o); err != nil {
				return nil, err
			}
			if o.Name != name {
				return nil, fmt.Errorf("plunge: the record at %q says it is for the output %q, but its key names %q",
					key, o.Name, name)
			}
			k.output = &o
			outputs[o.Name] = o.Value
		case strings.HasPrefix(key, keys.snippetPrefix()):
			var s snippetRecord
			if err := decodeRecord(key, rec.Payload, snippetFormat, &s); err != nil {
				return nil, err
			}
			k.snippet = &s
			snippets = append(snippets, s.Snippet)
			features["snippets"] = true
		case strings.HasPrefix(key, keys.extensionPrefix()):
			var e extensionRecord
			if err := decodeRecord(key, rec.Payload, extensionFormat, &e); err != nil {
				return nil, err
			}
			k.extension = &e
			extensions[e.Ref] = e.Extension
		default:
			return nil, fmt.Errorf("plunge: the record key %q is under the stack's namespace but is no kind of "+
				"record this version writes; refusing to guess what it holds", key)
		}
		l.records[key] = k
	}

	ordered, err := orderStates(resources, resourceKeys)
	if err != nil {
		return nil, err
	}

	// Merge the outputs back into the root Stack resource.
	if len(outputs) > 0 {
		for i := range ordered {
			if isRootStack(ordered[i]) {
				merged := make(map[string]any, len(outputs))
				for name, v := range outputs {
					merged[name] = v
				}
				ordered[i].Outputs = merged
				break
			}
		}
	}

	var pending []apitype.OperationV2
	for _, r := range resources {
		pending = append(pending, r.Pending...)
	}
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].Resource.URN < pending[j].Resource.URN })

	manifest := deploy.Manifest{Time: now, Version: version.Version}
	manifest.Magic = manifest.NewMagic()

	d := &apitype.DeploymentV3{
		Manifest:          manifest.Serialize(),
		SecretsProviders:  secrets,
		Resources:         ordered,
		PendingOperations: pending,
		Snippets:          snippets,
	}
	if len(extensions) > 0 {
		d.Extensions = extensions
	}

	l.deployment = apitype.TypedDeployment{Deployment: d, Version: apitype.DeploymentSchemaVersionCurrent}
	if len(features) > 0 {
		l.deployment.Version = apitype.DeploymentSchemaVersionLatest
		for f := range features {
			l.deployment.Features = append(l.deployment.Features, f)
		}
		sort.Strings(l.deployment.Features)
	}
	return l, nil
}

func decodeRecord(key string, payload []byte, format string, into any) error {
	var head struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(payload, &head); err != nil {
		return fmt.Errorf("plunge: the record at %q is not JSON: %w", key, err)
	}
	if head.Format != format {
		return fmt.Errorf("plunge: the record at %q names format %q where %q was expected; "+
			"this version of plunge does not read it", key, head.Format, format)
	}
	if err := json.Unmarshal(payload, into); err != nil {
		return fmt.Errorf("plunge: decoding the record at %q: %w", key, err)
	}
	return nil
}

// stateRef is one state taken out of a record, with what it refers to.
type stateRef struct {
	state    apitype.ResourceV3
	key      string
	seq      int
	refs     []resource.URN // parent, dependencies, property dependencies, deleted-with, replace-with
	provider string         // provider reference (URN::ID), "" for none
}

// orderStates checks every reference against the records and returns the states in an order VerifyIntegrity
// accepts: each after its parent, its provider and every resource it depends on. Ties are broken by URN and then by
// position in the record, so the same records always assemble to the same snapshot.
func orderStates(records []*resourceRecord, keys map[resource.URN]string) ([]apitype.ResourceV3, error) {
	urns := map[resource.URN]bool{}
	provs := map[string]bool{}
	var all []*stateRef
	for _, r := range records {
		for i, s := range r.States {
			urns[s.URN] = true
			if providers.IsProviderType(s.Type) {
				if ref, err := providers.NewReference(s.URN, s.ID); err == nil {
					provs[ref.String()] = true
				}
			}
			sr := &stateRef{state: s, key: keys[r.URN], seq: i}
			if s.Parent != "" {
				sr.refs = append(sr.refs, s.Parent)
			}
			sr.refs = append(sr.refs, s.Dependencies...)
			for _, deps := range s.PropertyDependencies {
				sr.refs = append(sr.refs, deps...)
			}
			if s.DeletedWith != "" {
				sr.refs = append(sr.refs, s.DeletedWith)
			}
			sr.refs = append(sr.refs, s.ReplaceWith...)
			if s.Provider != "" && !s.PendingReplacement {
				sr.provider = s.Provider
			}
			all = append(all, sr)
		}
	}

	// Refuse every dangling reference by name before ordering anything.
	var missing []MissingReference
	for _, sr := range all {
		s := sr.state
		if s.Parent != "" && !urns[s.Parent] {
			missing = append(missing, MissingReference{s.URN, "parent", string(s.Parent), sr.key})
		}
		for _, d := range s.Dependencies {
			if !urns[d] {
				missing = append(missing, MissingReference{s.URN, "dependency", string(d), sr.key})
			}
		}
		propKeys := make([]string, 0, len(s.PropertyDependencies))
		for k := range s.PropertyDependencies {
			propKeys = append(propKeys, string(k))
		}
		sort.Strings(propKeys)
		for _, k := range propKeys {
			for _, d := range s.PropertyDependencies[resource.PropertyKey(k)] {
				if !urns[d] {
					missing = append(missing, MissingReference{s.URN, "property dependency (" + k + ")", string(d), sr.key})
				}
			}
		}
		if s.DeletedWith != "" && !urns[s.DeletedWith] {
			missing = append(missing, MissingReference{s.URN, "deleted-with resource", string(s.DeletedWith), sr.key})
		}
		for _, d := range s.ReplaceWith {
			if !urns[d] {
				missing = append(missing, MissingReference{s.URN, "replace-with resource", string(d), sr.key})
			}
		}
		if sr.provider != "" {
			ref, err := providers.ParseReference(sr.provider)
			if err != nil || !provs[ref.String()] {
				missing = append(missing, MissingReference{s.URN, "provider", sr.provider, sr.key})
			}
		}
	}
	if len(missing) > 0 {
		return nil, &MissingReferenceError{Missing: missing}
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].state.URN != all[j].state.URN {
			return all[i].state.URN < all[j].state.URN
		}
		return all[i].seq < all[j].seq
	})

	// Emit in passes: a state is ready once some state of every URN it refers to, and the exact provider it names,
	// has been emitted.
	emittedURN := map[resource.URN]bool{}
	emittedProv := map[string]bool{}
	out := make([]apitype.ResourceV3, 0, len(all))
	remaining := all
	for len(remaining) > 0 {
		var next []*stateRef
		progressed := false
		for _, sr := range remaining {
			if !ready(sr, emittedURN, emittedProv) {
				next = append(next, sr)
				continue
			}
			out = append(out, sr.state)
			emittedURN[sr.state.URN] = true
			if providers.IsProviderType(sr.state.Type) {
				if ref, err := providers.NewReference(sr.state.URN, sr.state.ID); err == nil {
					emittedProv[ref.String()] = true
				}
			}
			progressed = true
		}
		if !progressed {
			seen := map[resource.URN]bool{}
			var cyc []resource.URN
			for _, sr := range next {
				if !seen[sr.state.URN] {
					seen[sr.state.URN] = true
					cyc = append(cyc, sr.state.URN)
				}
			}
			return nil, &CycleError{URNs: cyc}
		}
		remaining = next
	}
	return out, nil
}

func ready(sr *stateRef, urns map[resource.URN]bool, provs map[string]bool) bool {
	for _, u := range sr.refs {
		if !urns[u] {
			return false
		}
	}
	if sr.provider != "" {
		ref, err := providers.ParseReference(sr.provider)
		if err != nil || !provs[ref.String()] {
			return false
		}
	}
	return true
}
