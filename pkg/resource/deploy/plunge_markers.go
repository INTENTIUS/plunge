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

// The engine side of plunge's ownership markers (INTENTIUS/plunge#7). Three seams on Target, each nil for every
// upstream backend, so stock behaviour is unchanged unless a backend sets them:
//
//   - Target.Markers.Inject runs on every custom, non-provider registration after the resource's own, its
//     parents' and the stack's transforms (source_eval.go), so a user's stack transform cannot strip or rewrite a
//     marker. Default providers and ReadResource never reach it; component children do, through the same monitor.
//   - Target.Markers.CheckProvider runs on every provider resource, default or explicit, before its steps are
//     generated (step_generator.go), so a provider configuration that would hide markers is refused by name.
//   - Target.Binder.Bind runs for a custom, non-provider resource the base snapshot does not hold and the program
//     does not import, before its step is generated. A live object already carrying the resource's markers is
//     bound (the create becomes an import of it); two such objects are a collision, refused naming both IDs.

// MarkerPolicy injects ownership markers into a resource's inputs and vets provider configuration.
type MarkerPolicy interface {
	// Inject returns req.Properties with this run's markers merged in. Returning req.Properties unchanged is how a
	// policy declines a type (no tags, an unsupported tag shape, tags unknown during preview); it records why
	// itself. An error refuses the registration.
	Inject(ctx context.Context, req MarkerRequest) (resource.PropertyMap, error)

	// CheckProvider refuses a provider whose configuration would drop or override markers.
	CheckProvider(ctx context.Context, urn resource.URN, inputs resource.PropertyMap) error
}

// MarkerRequest is one registration handed to MarkerPolicy.Inject.
type MarkerRequest struct {
	URN  resource.URN
	Type tokens.Type
	// Provider is the plugin that will manage the resource, nil when it could not be resolved.
	Provider plugin.Provider
	// ProviderRef is the provider reference the registration resolved to.
	ProviderRef string
	// Properties are the inputs after every transform ran.
	Properties resource.PropertyMap
	DryRun     bool
}

// MarkerBinder binds a planned create to the live object already carrying its markers.
type MarkerBinder interface {
	// Bind returns the ID of the one live object carrying req.URN's markers, "" when there is none (or the type
	// has no consistent lister, which the binder records), and an error refusing the run when more than one does.
	Bind(ctx context.Context, req BindRequest) (resource.ID, error)
}

// BindRequest is one planned create handed to MarkerBinder.Bind.
type BindRequest struct {
	URN  resource.URN
	Type tokens.Type
	// Provider is the plugin that manages the resource.
	Provider plugin.Provider
	// ProviderURN and ProviderInputs are the provider resource's URN and inputs (region, project, subscription,
	// credentials), nil when the provider's state is not known to the step generator.
	ProviderURN    resource.URN
	ProviderInputs resource.PropertyMap
	// Inputs are the resource's inputs, markers included.
	Inputs resource.PropertyMap
	DryRun bool
}

// injectMarkers is the resource monitor's call into Target.Markers. It is a no-op without a policy and for
// anything but a custom, non-provider resource.
func (rm *resmon) injectMarkers(
	ctx context.Context, custom bool, t tokens.Type, name string, parent resource.URN,
	providerRef sdkproviders.Reference, props resource.PropertyMap,
) (resource.PropertyMap, error) {
	if rm.markers == nil || !custom || sdkproviders.IsProviderType(t) {
		return props, nil
	}
	parentType := tokens.Type("")
	if parent != "" && parent.QualifiedType() != resource.RootStackType {
		parentType = parent.QualifiedType()
	}
	urn := resource.NewURN(rm.stackName, rm.projectName, parentType, t, name)
	var prov plugin.Provider
	if providerRef != (sdkproviders.Reference{}) {
		prov, _ = rm.providers.GetProvider(providerRef)
	}
	return rm.markers.Inject(ctx, MarkerRequest{
		URN:         urn,
		Type:        t,
		Provider:    prov,
		ProviderRef: providerRef.String(),
		Properties:  props,
		DryRun:      rm.opts.DryRun,
	})
}

// checkProviderMarkers is the step generator's call into Target.Markers.CheckProvider, for provider resources.
func (sg *stepGenerator) checkProviderMarkers(ctx context.Context, urn resource.URN, goal *pkgresource.Goal) error {
	policy := sg.deployment.Target().Markers
	if policy == nil || !sdkproviders.IsProviderType(goal.Type) {
		return nil
	}
	if err := policy.CheckProvider(ctx, urn, resource.ToResourcePropertyMap(goal.Properties)); err != nil {
		sg.deployment.Diag().Errorf(diag.Message(urn, "%s"), err.Error())
		return result.BailError(err)
	}
	return nil
}

// bindMarkers is the step generator's call into Target.Binder for a planned create. It returns the bound ID, or ""
// when nothing is bound.
func (sg *stepGenerator) bindMarkers(
	ctx context.Context, urn resource.URN, goal *pkgresource.Goal, new *pkgresource.State, prov plugin.Provider,
) (resource.ID, error) {
	binder := sg.deployment.Target().Binder
	if binder == nil || goal.ID != "" || !goal.Custom || sdkproviders.IsProviderType(goal.Type) || prov == nil {
		return "", nil
	}
	req := BindRequest{
		URN:      urn,
		Type:     goal.Type,
		Provider: prov,
		Inputs:   new.Inputs,
		DryRun:   sg.deployment.opts.DryRun,
	}
	if goal.Provider != "" {
		if ref, err := sdkproviders.ParseReference(goal.Provider); err == nil {
			req.ProviderURN = ref.URN()
			if p, ok := sg.providers[ref.URN()]; ok && p != nil {
				req.ProviderInputs = p.Inputs
			}
		}
	}
	id, err := binder.Bind(ctx, req)
	if err != nil {
		sg.deployment.Diag().Errorf(diag.Message(urn, "%s"), err.Error())
		return "", result.BailError(err)
	}
	if id != "" {
		sg.deployment.Diag().Infof(diag.Message(urn,
			"plunge: bound to the live object %s, which already carries this resource's ownership markers"), id)
	}
	return id, nil
}
