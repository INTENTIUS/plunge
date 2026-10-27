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
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/result"
)

// The delete vet (INTENTIUS/plunge#37). A record is never read as permission to delete: before the step generator
// makes any delete step, Target.DeleteVetter is handed every object the run would delete, `destroy` included, and
// may refuse the run. It is nil for every upstream backend, so stock behaviour is unchanged unless a backend sets it.
//
// What it is asked about: every custom, non-provider state the run deletes because the program did not register it
// (or because this is a destroy), that DeleteGuard did not keep, that is not external (a read resource's delete
// touches nothing) and not retained on delete. Pending deletes left by a replacement are not asked: their delete
// finishes a replacement of this stack's own resource. With refresh on, each state handed over holds the outputs
// the refresh just read, so the vetter needs no provider call; with refresh off it reads the object once through
// the provider it is handed, the same Read a refresh makes under the stack's provider configuration.

// DeleteVetter vets the deletes a run is about to make.
type DeleteVetter interface {
	// VetDeletes returns an error refusing the run when any of req.Deletes is not this stack's to delete. It is
	// called once per run, before any delete step exists, and never when there is nothing to delete.
	VetDeletes(ctx context.Context, req DeleteVetRequest) error
}

// DeleteVetRequest is everything one run would delete.
type DeleteVetRequest struct {
	Deletes []DeleteCandidate
	// Refreshed reports that this run refreshed before generating its deletes, so each candidate's outputs are the
	// live object's as of this run.
	Refreshed bool
	DryRun    bool
	// Diag is the deployment's diagnostic sink, for the vetter's notes (a record-owned delete, say), which then
	// arrive as engine diagnostic events.
	Diag diag.Sink
}

// DeleteCandidate is one object a run would delete.
type DeleteCandidate struct {
	// State is the resource's state: the refreshed one when this run refreshed it.
	State *pkgresource.State
	// Provider is the plugin that manages the resource, nil when it could not be loaded.
	Provider plugin.Provider
	// ProviderURN and ProviderInputs are the provider resource's URN and inputs, nil when unknown.
	ProviderURN    resource.URN
	ProviderInputs resource.PropertyMap
}

// vetPlungeDeletes is the step generator's call into Target.DeleteVetter, from GenerateDeletes. guarded is what
// DeleteGuard keeps. A refusal is reported on the deployment's diagnostic sink and returned as a bail, so no
// delete step is generated.
func (sg *stepGenerator) vetPlungeDeletes(
	isTargeted func(*pkgresource.State) bool, guarded map[*pkgresource.State]bool,
) error {
	target := sg.deployment.target
	if target == nil || target.DeleteVetter == nil {
		return nil
	}

	providerStates := map[resource.URN]*pkgresource.State{}
	if prev := sg.deployment.prev; prev != nil {
		for _, res := range prev.Resources {
			if sdkproviders.IsProviderType(res.Type) && !res.Delete {
				providerStates[res.URN] = res
			}
		}
	}
	for urn, res := range sg.providers {
		providerStates[urn] = res
	}

	var deletes []DeleteCandidate
	seen := map[*pkgresource.State]bool{}
	add := func(res *pkgresource.State) error {
		if seen[res] || !res.Custom || sdkproviders.IsProviderType(res.Type) || res.External || res.RetainOnDelete ||
			res.ID == "" {
			return nil
		}
		seen[res] = true
		c := DeleteCandidate{State: res}
		if res.Provider != "" {
			if err := sg.deployment.EnsureProvider(res.Provider); err != nil {
				return err
			}
			if ref, err := sdkproviders.ParseReference(res.Provider); err == nil {
				c.Provider, _ = sg.deployment.GetProvider(ref)
				c.ProviderURN = ref.URN()
				if p, ok := providerStates[ref.URN()]; ok && p != nil {
					c.ProviderInputs = p.Inputs
				}
			}
		}
		deletes = append(deletes, c)
		return nil
	}

	if prev := sg.deployment.prev; prev != nil {
		for _, res := range prev.Resources {
			if res.ViewOf != "" || res.Delete || guarded[res] || sg.isOperatedOn(res.URN) || !isTargeted(res) {
				continue
			}
			if state, has := sg.refreshStates[res]; has {
				res = state
			}
			if err := add(res); err != nil {
				return err
			}
		}
	}
	// A destroy that runs the program deletes what it registered, too.
	for _, res := range sg.toDelete {
		if isTargeted(res) {
			if err := add(res); err != nil {
				return err
			}
		}
	}
	if len(deletes) == 0 {
		return nil
	}

	ctx := context.Background()
	if sg.deployment.ctx != nil && sg.deployment.ctx.Base() != nil {
		ctx = sg.deployment.ctx.Base()
	}
	err := target.DeleteVetter.VetDeletes(ctx, DeleteVetRequest{
		Deletes:   deletes,
		Refreshed: sg.deployment.opts.Refresh,
		DryRun:    sg.deployment.opts.DryRun,
		Diag:      sg.deployment.Diag(),
	})
	if err != nil {
		sg.deployment.Diag().Errorf(diag.RawMessage("", err.Error()))
		return result.BailError(err)
	}
	return nil
}
