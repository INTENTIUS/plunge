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

// The import vet (INTENTIUS/plunge#51): deploy.ImportVetter for the plunge backend. A resource the program
// registers with `import:` is not in the injector's base snapshot, so two of plunge's rules used to skip it: the
// create-only-tags refusal (inject.go) and the rule that another stack's marker is never written over. Before the
// import step exists:
//
//   - A type whose tags can only be set at create is refused by name, before any provider call: marking the
//     imported object would replace it, and plunge never replaces a resource to mark it. For such a type a marker
//     is required at import, and only the marker-bind phase can supply an object that already carries one, so an
//     explicit `import:` of it is always refused (choudoufu#2284's design, section 2: the type table first).
//   - Any other taggable type is read once through its provider's Read under the stack's own provider
//     configuration, the same call the import step makes next. A live object carrying another stack's marker, or
//     this stack's marker for another resource, is refused, as plunge#37 refuses such a delete. One carrying no
//     marker is imported, and the update that follows adds this stack's markers in place; that is what an import
//     into a plunge stack is for. One already carrying this resource's markers is imported as is.
//   - A record-owned type (no tags argument, an unsupported shape, markerless per pkg/identity) has no marker to
//     read or write, so it is imported on the program's word, as before.
//
// A create the bind phase turned into an import was found by its own markers, so it is passed without a read.

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// ImportRefusedError refuses a run that would import an object plunge must not import into this stack.
type ImportRefusedError struct {
	URN  resource.URN
	ID   resource.ID
	Type tokens.Type
	// Marker is the stack the live object's marker names, as written (a digest for the label encoding); empty when
	// the refusal is not about a marker.
	Marker string
	// Detail says what was wrong.
	Detail string
	// CreateOnly is set when the refusal is for a create-only-tags type.
	CreateOnly *CreateOnlyTagsError
}

func (e *ImportRefusedError) Error() string {
	return fmt.Sprintf("plunge refuses to import %s as %s (%s): %s. Nothing was imported", e.ID, e.URN, e.Type,
		e.Detail)
}

// Unwrap returns the create-only-tags refusal, when that is what this is.
func (e *ImportRefusedError) Unwrap() error {
	if e.CreateOnly == nil {
		return nil
	}
	return e.CreateOnly
}

// importVetter is deploy.ImportVetter for one run.
type importVetter struct {
	catalog *catalog
	// ledger is the run's ownership ledger, read for the ID the bind phase bound a create to.
	ledger *ledger
}

var _ deploy.ImportVetter = (*importVetter)(nil)

func newImportVetter(c *catalog, l *ledger) *importVetter {
	return &importVetter{catalog: c, ledger: l}
}

// VetImport implements deploy.ImportVetter.
func (v *importVetter) VetImport(ctx context.Context, req deploy.ImportVetRequest) error {
	if e, ok := v.ledger.get(req.URN); ok && e.Bound != "" && e.Bound == req.ID {
		return nil
	}
	refusal := &ImportRefusedError{URN: req.URN, ID: req.ID, Type: req.Type}

	info := v.catalog.lookup(ctx, req.Provider, req.Type)
	if info.Family == FamilyAzureNative && info.azureNativeFallback {
		if _, ok := req.Inputs[resource.PropertyKey(info.Property)]; ok {
			info.Shape, info.Reason = ShapeMap, ""
		}
	}
	if owned, _ := recordOwned(info); owned {
		return nil
	}
	if info.CreateOnly {
		refusal.CreateOnly = &CreateOnlyTagsError{URN: req.URN, Type: req.Type, Property: info.Property}
		refusal.Detail = fmt.Sprintf("%s's %q can only be set when the resource is created, so writing this "+
			"stack's ownership markers onto the imported object would replace it, and plunge never replaces a "+
			"resource to mark it. If the object already carries this resource's markers, drop `import:` and plunge "+
			"binds it by its markers where it can list the type; otherwise leave it out of this stack",
			req.Type, info.Property)
		return refusal
	}

	want, err := markers.For(req.URN, info.Encoding())
	if err != nil {
		refusal.Detail = err.Error()
		return refusal
	}
	if req.Provider == nil {
		refusal.Detail = "its provider is not loaded, so plunge cannot read the live object's marker"
		return refusal
	}
	resp, err := req.Provider.Read(ctx, plugin.ReadRequest{
		URN:  req.URN,
		Name: req.URN.Name(),
		Type: req.Type,
		ID:   req.ID,
	})
	switch {
	case err != nil:
		refusal.Detail = fmt.Sprintf("plunge could not read the live object to check its marker: %v", err)
		return refusal
	case resp.Outputs == nil:
		// Nothing there: the import step reports that itself.
		return nil
	}

	tags := tagsOf(resource.ToResourcePropertyMap(*resp.Outputs), info)
	marker, detail, _ := liveOwnerRefusal(tags, want)
	if detail == "" {
		// Unmarked, or already this resource's: the update after the import adds this stack's markers in place.
		return nil
	}
	refusal.Marker, refusal.Detail = marker, detail
	if marker != want[markers.TagStack] {
		refusal.Detail += ". Move it out of that stack first, or drop the `import:`"
	}
	return refusal
}

// liveOwnerRefusal reads a live object's tags against the markers want names. ours reports that the object
// already carries them. A non-empty detail refuses it: another stack's marker (marker is that stack, as written),
// or this stack's marker for another resource. An unmarked object is neither.
func liveOwnerRefusal(tags, want map[string]string) (marker, detail string, ours bool) {
	if markers.Carries(tags, want) {
		return "", "", true
	}
	mine := want[markers.TagStack]
	stack, urn, has := markers.Owner(tags)
	switch {
	case !has:
		return "", "", false
	case stack != mine:
		return stack, fmt.Sprintf("its live object's %s marker names the stack %q, not this stack's %q; "+
			"importing it would write this stack's markers over that stack's, and an object is one stack's",
			markers.TagStack, stack, mine), false
	default:
		return stack, fmt.Sprintf("its live object's markers name this stack but another resource (%s %q), "+
			"which still claims it", markers.TagURN, urn), false
	}
}
