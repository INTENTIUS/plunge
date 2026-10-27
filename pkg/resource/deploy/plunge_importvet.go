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

package deploy

import (
	"context"

	pkgresource "github.com/pulumi/pulumi/pkg/v3/resource"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	sdkproviders "github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/result"
)

// The import vet (INTENTIUS/plunge#51). A resource the program registers with `import:` is not in the base
// snapshot, so the marker injector's create-only-tags refusal (which checks the prior state) never fires for it,
// and its markers are merged over whatever the live object's tags hold. Target.ImportVetter is asked about every
// import of a custom, non-provider resource before the import step exists, and may refuse the run. It is nil for
// every upstream backend, so stock behaviour is unchanged unless a backend sets it.
//
// A create the marker-bind phase turned into an import (bindMarkers) is asked too; the plunge backend's vetter
// passes it without a read, since the bind found it by this resource's own markers.

// ImportVetter vets an import a run is about to make.
type ImportVetter interface {
	// VetImport returns an error refusing the run when req's object must not be imported into this stack.
	VetImport(ctx context.Context, req ImportVetRequest) error
}

// ImportVetRequest is one import, as the step generator is about to make it.
type ImportVetRequest struct {
	URN  resource.URN
	Type tokens.Type
	// ID is the ID the object is imported by.
	ID resource.ID
	// Provider is the plugin that manages the resource, nil when it could not be loaded.
	Provider plugin.Provider
	// ProviderURN and ProviderInputs are the provider resource's URN and inputs, nil when unknown.
	ProviderURN    resource.URN
	ProviderInputs resource.PropertyMap
	// Inputs are the resource's inputs after every transform and the marker injector ran.
	Inputs resource.PropertyMap
	DryRun bool
	// Diag is the deployment's diagnostic sink.
	Diag diag.Sink
}

// vetPlungeImport is the step generator's call into Target.ImportVetter, for an import it is about to make. A
// refusal is reported on the resource and returned as a bail, so no import step is generated.
func (sg *stepGenerator) vetPlungeImport(
	ctx context.Context, urn resource.URN, goal *pkgresource.Goal, prov plugin.Provider,
) error {
	target := sg.deployment.Target()
	if target == nil || target.ImportVetter == nil || !goal.Custom || sdkproviders.IsProviderType(goal.Type) ||
		goal.ID == "" {
		return nil
	}
	req := ImportVetRequest{
		URN:      urn,
		Type:     goal.Type,
		ID:       goal.ID,
		Provider: prov,
		Inputs:   resource.ToResourcePropertyMap(goal.Properties),
		DryRun:   sg.deployment.opts.DryRun,
		Diag:     sg.deployment.Diag(),
	}
	if goal.Provider != "" {
		if ref, err := sdkproviders.ParseReference(goal.Provider); err == nil {
			req.ProviderURN = ref.URN()
			if p, ok := sg.providers[ref.URN()]; ok && p != nil {
				req.ProviderInputs = p.Inputs
			}
		}
	}
	if err := target.ImportVetter.VetImport(ctx, req); err != nil {
		sg.deployment.Diag().Errorf(diag.Message(urn, "%s"), err.Error())
		return result.BailError(err)
	}
	return nil
}
