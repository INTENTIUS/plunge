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
)

// The delete vet (INTENTIUS/plunge#37). A record is never read as permission to delete: before the step generator
// makes any delete step, Target.DeleteVetter is handed every object the run would delete, `destroy` included, and
// may refuse the run. It is nil for every upstream backend, so stock behaviour is unchanged unless a backend sets it.
//
// What it is asked about: every custom, non-provider state the run deletes because the program did not register it
// (or because this is a destroy), that DeleteGuard did not keep, that is not external (a read resource's delete
// touches nothing) and not retained on delete. With refresh on, each state handed over holds the outputs the
// refresh just read, so the vetter needs no provider call; with refresh off it reads the object once through the
// provider it is handed, the same Read a refresh makes under the stack's provider configuration.
//
// The delete half of a replacement is asked too (INTENTIUS/plunge#51), with DeleteCandidate.Replacement set: a
// replacement deletes the original object, and a replace must not become the way an object without this stack's
// marker gets deleted (an `import:` whose markers force a new resource, say). The step generator asks when it
// decides to replace, before the replacement's first step exists, about the original and, for a
// delete-before-replace, every dependent it deletes first (vetPlungeReplaced). Pending deletes the snapshot already
// holds from an earlier run's replacement are asked in GenerateDeletes with the rest. A pending delete this run
// already asked about is not asked twice.

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
	// Replacement reports that the delete is the delete half of a replacement: the original object a replace
	// deletes, a delete-before-replace's dependent, or a pending delete left by an earlier run's replacement.
	Replacement bool
}

// plungeVetKey names one object for the replacement vet's bookkeeping. The original and its replacement share a
// URN, so the ID tells them apart.
func plungeVetKey(res *pkgresource.State) string {
	return string(res.URN) + "\x00" + string(res.ID)
}

// plungeProviderStates is every provider state the step generator knows: the base snapshot's, then this run's.
func (sg *stepGenerator) plungeProviderStates() map[resource.URN]*pkgresource.State {
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
	return providerStates
}

// plungeDeleteCandidate is res as a DeleteCandidate, or false when the vet is not asked about it: a component, a
// provider, a read resource, one retained on delete, or one with no ID.
func (sg *stepGenerator) plungeDeleteCandidate(
	res *pkgresource.State, providerStates map[resource.URN]*pkgresource.State, replacement bool,
) (DeleteCandidate, bool, error) {
	if !res.Custom || sdkproviders.IsProviderType(res.Type) || res.External || res.RetainOnDelete || res.ID == "" {
		return DeleteCandidate{}, false, nil
	}
	c := DeleteCandidate{State: res, Replacement: replacement}
	if res.Provider != "" {
		if err := sg.deployment.EnsureProvider(res.Provider); err != nil {
			return DeleteCandidate{}, false, err
		}
		if ref, err := sdkproviders.ParseReference(res.Provider); err == nil {
			c.Provider, _ = sg.deployment.GetProvider(ref)
			c.ProviderURN = ref.URN()
			if p, ok := providerStates[ref.URN()]; ok && p != nil {
				c.ProviderInputs = p.Inputs
			}
		}
	}
	return c, true, nil
}

// askPlungeDeleteVetter hands deletes to Target.DeleteVetter. A refusal is returned as a plain error, not a bail:
// every caller's executor reports a plain error once and fails the run, while the post-step path that runs
// GenerateDeletes drops a bail without failing the run (deploymentExecutor.Execute), which let a refused
// `up` delete succeed (INTENTIUS/plunge#37).
func (sg *stepGenerator) askPlungeDeleteVetter(deletes []DeleteCandidate) error {
	if len(deletes) == 0 {
		return nil
	}
	ctx := context.Background()
	if sg.deployment.ctx != nil && sg.deployment.ctx.Base() != nil {
		ctx = sg.deployment.ctx.Base()
	}
	err := sg.deployment.target.DeleteVetter.VetDeletes(ctx, DeleteVetRequest{
		Deletes:   deletes,
		Refreshed: sg.deployment.opts.Refresh,
		DryRun:    sg.deployment.opts.DryRun,
		Diag:      sg.deployment.Diag(),
	})
	if err != nil {
		return err
	}
	return nil
}

// vetPlungeReplaced is the step generator's call into Target.DeleteVetter when it decides to replace urn
// (INTENTIUS/plunge#51), before any of the replacement's steps exist: old is the original the replacement deletes
// (nil when only dependents are asked), dependents are what a delete-before-replace deletes first. An original
// already pending replacement has been deleted, so it is not asked.
func (sg *stepGenerator) vetPlungeReplaced(old *pkgresource.State, dependents []dependentReplace) error {
	target := sg.deployment.target
	if target == nil || target.DeleteVetter == nil {
		return nil
	}
	states := make([]*pkgresource.State, 0, len(dependents)+1)
	if old != nil {
		states = append(states, old)
	}
	for _, d := range dependents {
		if !sg.pendingDeletes[d.res] {
			states = append(states, d.res)
		}
	}
	providerStates := sg.plungeProviderStates()
	var deletes []DeleteCandidate
	for _, res := range states {
		if res == nil || res.PendingReplacement || sg.plungeVetted[plungeVetKey(res)] {
			continue
		}
		c, ok, err := sg.plungeDeleteCandidate(res, providerStates, true)
		if err != nil {
			return err
		}
		if ok {
			deletes = append(deletes, c)
		}
	}
	if err := sg.askPlungeDeleteVetter(deletes); err != nil {
		return err
	}
	if sg.plungeVetted == nil {
		sg.plungeVetted = map[string]bool{}
	}
	for _, c := range deletes {
		sg.plungeVetted[plungeVetKey(c.State)] = true
	}
	return nil
}

// vetPlungeDeletes is the step generator's call into Target.DeleteVetter, from GenerateDeletes. guarded is what
// DeleteGuard keeps. A refusal is returned as an error, so no delete step is generated and the run fails.
func (sg *stepGenerator) vetPlungeDeletes(
	isTargeted func(*pkgresource.State) bool, guarded map[*pkgresource.State]bool,
) error {
	target := sg.deployment.target
	if target == nil || target.DeleteVetter == nil {
		return nil
	}

	providerStates := sg.plungeProviderStates()

	var deletes []DeleteCandidate
	seen := map[*pkgresource.State]bool{}
	add := func(res *pkgresource.State, replacement bool) error {
		if seen[res] {
			return nil
		}
		seen[res] = true
		c, ok, err := sg.plungeDeleteCandidate(res, providerStates, replacement)
		if ok {
			deletes = append(deletes, c)
		}
		return err
	}

	if prev := sg.deployment.prev; prev != nil {
		for _, res := range prev.Resources {
			if res.ViewOf != "" || guarded[res] || !isTargeted(res) {
				continue
			}
			if state, has := sg.refreshStates[res]; has {
				res = state
			}
			// A pending delete finishes a replacement (INTENTIUS/plunge#51): asked unless it was deleted earlier in
			// this run or this run asked about it when it decided the replacement.
			replacement := res.Delete
			if replacement {
				if sg.pendingDeletes[res] || res.PendingReplacement || sg.plungeVetted[plungeVetKey(res)] {
					continue
				}
			} else if sg.isOperatedOn(res.URN) {
				continue
			}
			if err := add(res, replacement); err != nil {
				return err
			}
		}
	}
	// A destroy that runs the program deletes what it registered, too.
	for _, res := range sg.toDelete {
		if isTargeted(res) {
			if err := add(res, false); err != nil {
				return err
			}
		}
	}
	return sg.askPlungeDeleteVetter(deletes)
}
