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
	"github.com/pulumi/pulumi/pkg/v3/resource/autonaming"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	sdkproviders "github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/result"
)

// The engine side of plunge's autonaming policy (INTENTIUS/plunge#7, claim C1 of the parity table on #2).
// Target.Autonames, nil for every upstream backend, is asked for every custom, non-provider resource just before
// its Check, after the user's own `pulumi:autonaming` config has answered. It can replace the autonaming options
// the provider is given and force delete-before-replace. The step generator warns once per URN when a
// replacement is delete-before-replace only because the policy forced it.

// AutonamePolicy decides a custom resource's autonaming options over the user's `pulumi:autonaming` config.
type AutonamePolicy interface {
	// Autoname returns the options the provider's Check is given in place of req.User (nil keeps req.User), and
	// whether a replacement of the resource must delete it before creating the new one. An error refuses the
	// registration.
	Autoname(ctx context.Context, req AutonameRequest) (*plugin.AutonamingOptions, bool, error)
}

// AutonameRequest is one resource handed to AutonamePolicy.Autoname.
type AutonameRequest struct {
	URN  resource.URN
	Type tokens.Type
	// Provider is the plugin that manages the resource.
	Provider plugin.Provider
	// Inputs are the resource's inputs before Check, markers included.
	Inputs resource.PropertyMap
	// User is what the user's `pulumi:autonaming` config answered for this resource; nil when it sets nothing that
	// applies, which is upstream's default (a random suffix).
	User *plugin.AutonamingOptions
	// UserRandom lists the random expressions (${hex(8)}, ${uuid}, ...) in the user's pattern for this type.
	UserRandom []string
	DryRun     bool
}

// plungeAutoname is the step generator's call into Target.Autonames. It updates *opts and goal in place.
func (sg *stepGenerator) plungeAutoname(
	ctx context.Context, urn resource.URN, goal *pkgresource.Goal, inputs resource.PropertyMap,
	prov plugin.Provider, opts **plugin.AutonamingOptions,
) error {
	target := sg.deployment.Target()
	if target == nil {
		return nil
	}
	policy := target.Autonames
	if policy == nil || !goal.Custom || sdkproviders.IsProviderType(goal.Type) || prov == nil {
		return nil
	}
	var random []string
	if *opts != nil && sg.deployment.opts.Autonamer != nil {
		random = autonaming.RandomExpressions(sg.deployment.opts.Autonamer, goal.Type)
	}
	chosen, deleteFirst, err := policy.Autoname(ctx, AutonameRequest{
		URN:        urn,
		Type:       goal.Type,
		Provider:   prov,
		Inputs:     inputs,
		User:       *opts,
		UserRandom: random,
		DryRun:     sg.deployment.opts.DryRun,
	})
	if err != nil {
		sg.deployment.Diag().Errorf(diag.Message(urn, "%s"), err.Error())
		return result.BailError(err)
	}
	if chosen != nil {
		*opts = chosen
	}
	if deleteFirst {
		t := true
		goal.DeleteBeforeReplace = &t
		if sg.plungeDeleteFirst == nil {
			sg.plungeDeleteFirst = map[resource.URN]bool{}
		}
		sg.plungeDeleteFirst[urn] = true
	}
	return nil
}

// warnPlungeDeleteFirst warns, once per URN, that a replacement deletes first only because plunge's autonaming
// policy forced it: the provider alone would have created the replacement first.
func (sg *stepGenerator) warnPlungeDeleteFirst(urn resource.URN, providerWanted bool) {
	if providerWanted || !sg.plungeDeleteFirst[urn] {
		return
	}
	delete(sg.plungeDeleteFirst, urn)
	sg.deployment.Diag().Warningf(diag.Message(urn,
		"plunge: %s has a deterministic name, so its replacement deletes it before creating the new one (a "+
			"replacement with the same name cannot exist alongside it); expect a gap while it is replaced"), urn)
}
