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

package lifecycletest

// Tests for the two engine seams the plunge record backend sets on deploy.Target (INTENTIUS/plunge#6):
// DeleteGuard, which keeps a resource the program no longer registers, and ProviderIDs, which replaces the random
// UUID a new provider gets.

import (
	"context"
	"sync"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/pulumi/pulumi/pkg/v3/engine"
	lt "github.com/pulumi/pulumi/pkg/v3/engine/lifecycletest/framework"
	pkgresource "github.com/pulumi/pulumi/pkg/v3/resource"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// An older program that no longer registers a resource a newer run created must not delete it when the target's
// DeleteGuard says so, and must keep what that resource depends on.
func TestDeleteGuardKeepsResourceAndItsDependencies(t *testing.T) {
	t.Parallel()

	var deleted sync.Map
	loaders := []*deploytest.ProviderLoader{
		deploytest.NewProviderLoader("pkgA", semver.MustParse("1.0.0"), func() (plugin.Provider, error) {
			return &deploytest.Provider{
				DeleteF: func(_ context.Context, req plugin.DeleteRequest) (plugin.DeleteResponse, error) {
					deleted.Store(req.URN.Name(), true)
					return plugin.DeleteResponse{}, nil
				},
			}, nil
		}),
	}

	// The "newer" program registers resA, resB and resC (which depends on resB). The "older" program only knows
	// resA, plus resD, which it no longer registers either and nothing protects.
	newer := true
	programF := deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		_, err := monitor.RegisterResource("pkgA:m:typA", "resA", true)
		require.NoError(t, err)
		if newer {
			b, err := monitor.RegisterResource("pkgA:m:typA", "resB", true)
			require.NoError(t, err)
			_, err = monitor.RegisterResource("pkgA:m:typA", "resC", true, deploytest.ResourceOptions{
				Dependencies: []resource.URN{b.URN},
			})
			require.NoError(t, err)
			_, err = monitor.RegisterResource("pkgA:m:typA", "resD", true)
			require.NoError(t, err)
		}
		return nil
	})
	hostF := deploytest.NewPluginHostF(nil, nil, programF, nil, nil, loaders...)
	p := &lt.TestPlan{Options: lt.TestUpdateOptions{T: t, HostF: hostF}}
	project := p.GetProject()

	snap, err := lt.TestOp(Update).RunStep(project, p.GetTarget(t, nil), p.Options, false, p.BackendClient, nil, "0")
	require.NoError(t, err)
	require.Len(t, snap.Resources, 5) // default provider + four resources

	// The older run: the guard protects resC only. resB stays because resC depends on it; resD goes.
	newer = false
	target := p.GetTarget(t, snap)
	target.DeleteGuard = func(res *pkgresource.State) string {
		if res.URN.Name() == "resC" {
			return "created by a newer program version"
		}
		return ""
	}
	snap, err = lt.TestOp(Update).RunStep(project, target, p.Options, false, p.BackendClient, nil, "1")
	require.NoError(t, err)

	names := map[string]bool{}
	for _, res := range snap.Resources {
		names[res.URN.Name()] = true
	}
	assert.True(t, names["resA"])
	assert.True(t, names["resB"], "resB is kept because the guarded resC depends on it")
	assert.True(t, names["resC"], "resC is kept by the guard")
	assert.False(t, names["resD"], "resD is not guarded and is deleted")

	_, deletedB := deleted.Load("resB")
	_, deletedC := deleted.Load("resC")
	_, deletedD := deleted.Load("resD")
	assert.False(t, deletedB)
	assert.False(t, deletedC)
	assert.True(t, deletedD)

	require.NoError(t, snap.VerifyIntegrity())
}

// A target's ProviderIDs names the ID of a new default provider, so two runs that each create it from nothing
// agree on its URN::ID reference.
func TestProviderIDsGivesTwoRunsOneDefaultProviderID(t *testing.T) {
	t.Parallel()

	loaders := []*deploytest.ProviderLoader{
		deploytest.NewProviderLoader("pkgA", semver.MustParse("1.0.0"), func() (plugin.Provider, error) {
			return &deploytest.Provider{}, nil
		}),
	}
	programF := deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		_, err := monitor.RegisterResource("pkgA:m:typA", "resA", true)
		require.NoError(t, err)
		return nil
	})
	hostF := deploytest.NewPluginHostF(nil, nil, programF, nil, nil, loaders...)
	p := &lt.TestPlan{Options: lt.TestUpdateOptions{T: t, HostF: hostF}}
	project := p.GetProject()

	gen := func(urn resource.URN, _ resource.PropertyMap) (resource.ID, error) {
		return resource.ID("derived-" + urn.Name()), nil
	}

	providerID := func(name string) resource.ID {
		target := p.GetTarget(t, nil)
		target.ProviderIDs = gen
		snap, err := lt.TestOp(Update).RunStep(project, target, p.Options, false, p.BackendClient, nil, name)
		require.NoError(t, err)
		for _, res := range snap.Resources {
			if providers.IsProviderType(res.Type) {
				return res.ID
			}
		}
		t.Fatalf("no provider in the snapshot")
		return ""
	}

	first := providerID("0")
	second := providerID("1")
	assert.Equal(t, resource.ID("derived-default"), first)
	assert.Equal(t, first, second)
}
