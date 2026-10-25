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

// Tests for the marker seams the plunge record backend sets on deploy.Target (INTENTIUS/plunge#7): Markers, which
// injects ownership markers after every transform, and Binder, which binds a planned create to a marked live
// object or refuses a collision. Written for P-L4, not run in that unit.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/pulumi/pulumi/pkg/v3/engine"
	lt "github.com/pulumi/pulumi/pkg/v3/engine/lifecycletest/framework"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// tagPolicy merges a "plunge-urn" tag and records every call.
type tagPolicy struct {
	mu        sync.Mutex
	seen      []resource.URN
	providers []resource.URN
	refuse    error
}

func (p *tagPolicy) Inject(_ context.Context, req deploy.MarkerRequest) (resource.PropertyMap, error) {
	p.mu.Lock()
	p.seen = append(p.seen, req.URN)
	p.mu.Unlock()
	out := req.Properties.Copy()
	tags := resource.PropertyMap{}
	if v, ok := out["tags"]; ok && v.IsObject() {
		tags = v.ObjectValue().Copy()
	}
	tags["plunge-urn"] = resource.NewProperty(string(req.URN))
	out["tags"] = resource.NewProperty(tags)
	return out, nil
}

func (p *tagPolicy) CheckProvider(_ context.Context, urn resource.URN, _ resource.PropertyMap) error {
	p.mu.Lock()
	p.providers = append(p.providers, urn)
	p.mu.Unlock()
	return p.refuse
}

// The injector runs after the stack transform, so a transform that replaces the tags does not lose the marker;
// it runs on custom resources only, and every provider (the default one included) is vetted.
func TestPlungeMarkersRunAfterStackTransforms(t *testing.T) {
	t.Parallel()

	loaders := []*deploytest.ProviderLoader{
		deploytest.NewProviderLoader("pkgA", semver.MustParse("1.0.0"), func() (plugin.Provider, error) {
			return &deploytest.Provider{}, nil
		}),
	}
	programF := deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		callbacks, err := deploytest.NewCallbacksServer()
		require.NoError(t, err)
		defer func() { require.NoError(t, callbacks.Close()) }()

		strip, err := callbacks.Allocate(
			TransformFunction(func(name, typ string, custom bool, parent string,
				props resource.PropertyMap, opts *pulumirpc.TransformResourceOptions,
			) (resource.PropertyMap, *pulumirpc.TransformResourceOptions, error) {
				props["tags"] = resource.NewProperty(resource.PropertyMap{"team": resource.NewProperty("a")})
				return props, opts, nil
			}))
		require.NoError(t, err)
		require.NoError(t, monitor.RegisterStackTransform(strip))

		comp, err := monitor.RegisterResource("my:comp:Comp", "comp", false)
		require.NoError(t, err)
		_, err = monitor.RegisterResource("pkgA:m:typA", "resA", true, deploytest.ResourceOptions{
			Parent: comp.URN,
		})
		require.NoError(t, err)
		return nil
	})
	hostF := deploytest.NewPluginHostF(nil, nil, programF, nil, nil, loaders...)
	p := &lt.TestPlan{Options: lt.TestUpdateOptions{T: t, HostF: hostF}}
	project := p.GetProject()

	policy := &tagPolicy{}
	target := p.GetTarget(t, nil)
	target.Markers = policy
	snap, err := lt.TestOp(Update).RunStep(project, target, p.Options, false, p.BackendClient, nil, "0")
	require.NoError(t, err)

	for _, res := range snap.Resources {
		if res.URN.Name() != "resA" {
			continue
		}
		tags := res.Inputs["tags"].ObjectValue()
		assert.Equal(t, "a", tags["team"].StringValue(), "the transform ran")
		assert.Equal(t, string(res.URN), tags["plunge-urn"].StringValue(), "and the marker survived it")
	}
	require.Len(t, policy.seen, 1, "only the custom resource is handed to the injector, not the component")
	assert.Equal(t, "resA", policy.seen[0].Name())
	require.NotEmpty(t, policy.providers, "the default provider is vetted")
	assert.True(t, providers.IsProviderType(policy.providers[0].Type()))
}

// A provider the policy refuses stops the run before anything is created through it.
func TestPlungeMarkersRefuseAProvider(t *testing.T) {
	t.Parallel()

	created := false
	loaders := []*deploytest.ProviderLoader{
		deploytest.NewProviderLoader("pkgA", semver.MustParse("1.0.0"), func() (plugin.Provider, error) {
			return &deploytest.Provider{
				CreateF: func(context.Context, plugin.CreateRequest) (plugin.CreateResponse, error) {
					created = true
					return plugin.CreateResponse{ID: "x", Status: resource.StatusOK}, nil
				},
			}, nil
		}),
	}
	programF := deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		_, err := monitor.RegisterResource("pkgA:m:typA", "resA", true)
		return err
	})
	hostF := deploytest.NewPluginHostF(nil, nil, programF, nil, nil, loaders...)
	p := &lt.TestPlan{Options: lt.TestUpdateOptions{T: t, HostF: hostF}}

	target := p.GetTarget(t, nil)
	target.Markers = &tagPolicy{refuse: errors.New("ignoreTags hides the markers")}
	_, err := lt.TestOp(Update).RunStep(p.GetProject(), target, p.Options, false, p.BackendClient, nil, "0")
	require.Error(t, err)
	assert.False(t, created)
}

// fixedBinder answers every bind with the IDs it holds: none, one (bind) or several (collision).
type fixedBinder struct{ ids []resource.ID }

func (b *fixedBinder) Bind(_ context.Context, req deploy.BindRequest) (resource.ID, error) {
	switch len(b.ids) {
	case 0:
		return "", nil
	case 1:
		return b.ids[0], nil
	}
	return "", errors.New("collision: " + string(b.ids[0]) + " and " + string(b.ids[1]))
}

// One marked live object turns the create into an import of it; two refuse the run and create nothing.
func TestPlungeBinderBindsOrRefuses(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	creates := 0
	loaders := []*deploytest.ProviderLoader{
		deploytest.NewProviderLoader("pkgA", semver.MustParse("1.0.0"), func() (plugin.Provider, error) {
			return &deploytest.Provider{
				CreateF: func(context.Context, plugin.CreateRequest) (plugin.CreateResponse, error) {
					mu.Lock()
					defer mu.Unlock()
					creates++
					return plugin.CreateResponse{ID: "new", Status: resource.StatusOK}, nil
				},
			}, nil
		}),
	}
	programF := deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		_, err := monitor.RegisterResource("pkgA:m:typA", "resA", true)
		return err
	})
	hostF := deploytest.NewPluginHostF(nil, nil, programF, nil, nil, loaders...)
	p := &lt.TestPlan{Options: lt.TestUpdateOptions{T: t, HostF: hostF}}

	target := p.GetTarget(t, nil)
	target.Binder = &fixedBinder{ids: []resource.ID{"live-1"}}
	snap, err := lt.TestOp(Update).RunStep(p.GetProject(), target, p.Options, false, p.BackendClient, nil, "0")
	require.NoError(t, err)
	assert.Zero(t, creates, "a bound create is an import, not a create")
	for _, res := range snap.Resources {
		if res.URN.Name() == "resA" {
			assert.Equal(t, resource.ID("live-1"), res.ID)
		}
	}

	target = p.GetTarget(t, nil)
	target.Binder = &fixedBinder{ids: []resource.ID{"live-1", "live-2"}}
	_, err = lt.TestOp(Update).RunStep(p.GetProject(), target, p.Options, false, p.BackendClient, nil, "1")
	require.Error(t, err)
	assert.Zero(t, creates, "a collision creates nothing")
}
