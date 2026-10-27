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

// The delete vet (INTENTIUS/plunge#37): deploy.DeleteVetter for the plunge backend. choudoufu's rule is that a
// record is never read as permission to delete; the marker decides ownership. plunge now holds deletes to the same
// rule, `destroy` included.
//
// For every object a run would delete:
//
//   - A type that carries no marker (no tags argument, an unsupported tags shape, a schema plunge could not read, a
//     type pkg/identity classes markerless) is record-owned. Its record's ownership entry says so (by "record",
//     written by the injector when the resource was created or last updated), and it is deleted on the record's
//     word, with a note on the resource.
//   - Any other type must carry this stack's markers on its live object. When the run refreshed (the default for
//     update and preview, PLUNGE_REFRESH), the refreshed outputs hold the tags or labels and no provider call is
//     made. Otherwise the object is read once, through its provider's Read under the stack's own provider
//     configuration: the same call a refresh makes, so it goes to the endpoints the stack names (plunge#27) and
//     nowhere else. An object the read finds gone is deleted as today.
//   - A marker naming another stack (or another resource of this one), or no marker at all, refuses the run before
//     any delete step exists, ruled 2026-10-26 ("refuse, like choudoufu"). The refusal names every such resource,
//     its object ID, what its marker says, its record and the command that removes the record.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// DeleteRefusal is one delete the vet refused.
type DeleteRefusal struct {
	URN  resource.URN
	ID   resource.ID
	Type tokens.Type
	// Record is the key of the record that names the object.
	Record string
	// Marker is the stack the live object's marker names, as written (a digest for the label encoding); empty when
	// the object carries no marker.
	Marker string
	// Detail says what was wrong.
	Detail string
}

// DeleteRefusedError refuses a run that would delete objects whose live markers do not name this stack.
type DeleteRefusedError struct {
	Refusals []DeleteRefusal
}

func (e *DeleteRefusedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "plunge refuses this run: %d delete(s) would remove an object whose live marker does not name "+
		"this stack, and a record alone is not permission to delete. Nothing was deleted.", len(e.Refusals))
	for _, r := range e.Refusals {
		fmt.Fprintf(&b, "\n  - %s (ID %s): %s; its record is %s. If the object is no longer this stack's, remove "+
			"the record with `plunge state delete '%s'` and run again", r.URN, r.ID, r.Detail, r.Record, r.URN)
	}
	return b.String()
}

// deleteVetter is deploy.DeleteVetter over one run's records.
type deleteVetter struct {
	catalog *catalog
	keys    stackKeys
	// owners is each record's ownership entry, by URN; absent for records written before ownership entries or with
	// markers off.
	owners map[resource.URN]*ownershipEntry
}

var _ deploy.DeleteVetter = (*deleteVetter)(nil)

func newDeleteVetter(c *catalog, l *loaded) *deleteVetter {
	v := &deleteVetter{catalog: c, owners: map[resource.URN]*ownershipEntry{}}
	if l != nil {
		v.keys = l.keys
		for _, k := range l.records {
			if k.resource != nil && k.resource.Ownership != nil {
				v.owners[k.resource.URN] = k.resource.Ownership
			}
		}
	}
	return v
}

// VetDeletes implements deploy.DeleteVetter.
func (v *deleteVetter) VetDeletes(ctx context.Context, req deploy.DeleteVetRequest) error {
	var refused []DeleteRefusal
	for _, c := range req.Deletes {
		if r, ok := v.vet(ctx, req, c); !ok {
			refused = append(refused, r)
		}
	}
	if len(refused) == 0 {
		return nil
	}
	sort.Slice(refused, func(i, j int) bool { return refused[i].URN < refused[j].URN })
	return &DeleteRefusedError{Refusals: refused}
}

// recordOwned reports whether info's type carries no marker, and why.
func recordOwned(info TypeInfo) (bool, string) {
	if info.Markerless {
		return true, "pkg/identity classes " + info.TFType + " as markerless"
	}
	switch info.Shape {
	case ShapeMap, ShapeKeyValueArray:
		return false, ""
	}
	reason := info.Reason
	if reason == "" {
		reason = fmt.Sprintf("its tags shape is %q", info.Shape)
	}
	return true, reason
}

// vet decides one delete: ok when it may go ahead, else the refusal.
func (v *deleteVetter) vet(
	ctx context.Context, req deploy.DeleteVetRequest, c deploy.DeleteCandidate,
) (DeleteRefusal, bool) {
	st := c.State
	refusal := DeleteRefusal{URN: st.URN, ID: st.ID, Type: st.Type, Record: v.keys.resource(st.URN)}
	note := func(format string, args ...any) {
		if req.Diag != nil {
			req.Diag.Infof(diag.Message(st.URN, format), args...)
		}
	}

	entry := v.owners[st.URN]
	if entry != nil && entry.By == OwnershipRecord {
		reason := entry.Reason
		if reason == "" {
			reason = string(entry.State)
		}
		note("plunge: deleting on its record's word: the record's ownership entry says the type carries no marker (%s)",
			reason)
		return refusal, true
	}

	info := v.catalog.lookup(ctx, c.Provider, st.Type)
	if entry != nil && entry.By == OwnershipMarker && info.Family == FamilyAzureNative && info.azureNativeFallback {
		// The injector marked it through the program's own tags map, which proves the type has one.
		info.Shape, info.Reason = ShapeMap, ""
	}
	if owned, reason := recordOwned(info); owned {
		if entry == nil {
			note("plunge: deleting on its record's word: %s carries no ownership marker (%s)", st.Type, reason)
			return refusal, true
		}
		refusal.Detail = fmt.Sprintf("its record says the live object carries this stack's markers, but plunge "+
			"cannot tell where they are on %s (%s), so it cannot check them", st.Type, reason)
		return refusal, false
	}

	want, err := markers.For(st.URN, info.Encoding())
	if err != nil {
		refusal.Detail = err.Error()
		return refusal, false
	}

	outputs := st.Outputs
	if !req.Refreshed {
		if c.Provider == nil {
			refusal.Detail = "its provider is not loaded, so plunge cannot read the live object's marker"
			return refusal, false
		}
		resp, err := c.Provider.Read(ctx, plugin.ReadRequest{
			URN:    st.URN,
			Name:   st.URN.Name(),
			Type:   st.Type,
			ID:     st.ID,
			Inputs: resource.FromResourcePropertyMap(st.Inputs),
			State:  resource.FromResourcePropertyMap(st.Outputs),
		})
		switch {
		case err != nil:
			refusal.Detail = fmt.Sprintf("plunge could not read the live object to check its marker: %v", err)
			return refusal, false
		case resp.Outputs == nil:
			// Already gone: nothing of anyone's is deleted, as before.
			note("plunge: the live object %s is already gone", st.ID)
			return refusal, true
		}
		outputs = resource.ToResourcePropertyMap(*resp.Outputs)
	}

	tags := tagsOf(outputs, info)
	if markers.Carries(tags, want) {
		return refusal, true
	}
	ours := want[markers.TagStack]
	stack, urn, has := markers.Owner(tags)
	switch {
	case !has:
		refusal.Detail = fmt.Sprintf("its live object carries no %s marker, though %s takes one; an object without "+
			"this stack's marker is not this stack's, whatever the record says", markers.TagStack, st.Type)
	case stack != ours:
		refusal.Marker = stack
		refusal.Detail = fmt.Sprintf("its live object's %s marker names the stack %q, not this stack's %q",
			markers.TagStack, stack, ours)
	default:
		refusal.Marker = stack
		refusal.Detail = fmt.Sprintf("its live object's markers name this stack but another resource (%s %q)",
			markers.TagURN, urn)
	}
	return refusal, false
}
