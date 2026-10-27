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

// Tests for the seams INTENTIUS/plunge#51 adds to deploy.Target's plunge hooks: Target.ImportVetter is asked about
// every import before its step exists, and Target.DeleteVetter is asked about the delete half of a replacement
// (create-before-delete, delete-before-replace, and a pending delete an earlier run left), with
// DeleteCandidate.Replacement set. A refusal stops the run. Fakes only. Written for plunge#51, not run in that unit.

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
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// replaceVetter records every delete it is asked about, with its Replacement flag, and refuses when refuse is set.
type replaceVetter struct {
	mu     sync.Mutex
	asked  []deploy.DeleteCandidate
	refuse bool
}

func (v *replaceVetter) VetDeletes(_ context.Context, req deploy.DeleteVetRequest) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.asked = append(v.asked, req.Deletes...)
	if v.refuse {
		return errors.New("the original's live marker names another stack")
	}
	return nil
}

// importVetter records every import it is asked about and refuses when refuse is set.
type importVetter struct {
	mu     sync.Mutex
	asked  []deploy.ImportVetRequest
	refuse bool
}

func (v *importVetter) VetImport(_ context.Context, req deploy.ImportVetRequest) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.asked = append(v.asked, req)
	if v.refuse {
		return errors.New("create-only tags")
	}
	return nil
}

// replaceCloud is pkgA: a changed "foo" replaces; creates, deletes and reads are counted, and deletes fail while
// failDeletes is set.
type replaceCloud struct {
	mu          sync.Mutex
	creates     int
	deletes     int
	reads       int
	failDeletes bool
}

func (c *replaceCloud) counts() (creates, deletes, reads int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.creates, c.deletes, c.reads
}

func (c *replaceCloud) loaders() []*deploytest.ProviderLoader {
	return []*deploytest.ProviderLoader{
		deploytest.NewProviderLoader("pkgA", semver.MustParse("1.0.0"), func() (plugin.Provider, error) {
			return &deploytest.Provider{
				DiffF: func(_ context.Context, req plugin.DiffRequest) (plugin.DiffResult, error) {
					olds, news := resource.ToResourcePropertyMap(req.OldInputs), resource.ToResourcePropertyMap(req.NewInputs)
					if olds["foo"].DeepEquals(news["foo"]) {
						return plugin.DiffResult{Changes: plugin.DiffNone}, nil
					}
					return plugin.DiffResult{
						Changes: plugin.DiffSome, ChangedKeys: []resource.PropertyKey{"foo"},
						ReplaceKeys: []resource.PropertyKey{"foo"},
					}, nil
				},
				CreateF: func(_ context.Context, req plugin.CreateRequest) (plugin.CreateResponse, error) {
					c.mu.Lock()
					defer c.mu.Unlock()
					c.creates++
					return plugin.CreateResponse{
						ID: resource.ID(req.URN.Name() + "-" + string(rune('0'+c.creates))), Properties: req.Properties,
						Status: resource.StatusOK,
					}, nil
				},
				DeleteF: func(context.Context, plugin.DeleteRequest) (plugin.DeleteResponse, error) {
					c.mu.Lock()
					defer c.mu.Unlock()
					if c.failDeletes {
						return plugin.DeleteResponse{}, errors.New("delete failed")
					}
					c.deletes++
					return plugin.DeleteResponse{}, nil
				},
				ReadF: func(_ context.Context, req plugin.ReadRequest) (plugin.ReadResponse, error) {
					c.mu.Lock()
					defer c.mu.Unlock()
					c.reads++
					props := resource.FromResourcePropertyMap(resource.NewPropertyMapFromMap(map[string]any{"foo": "a"}))
					return plugin.ReadResponse{
						ReadResult: plugin.ReadResult{ID: req.ID, Inputs: &props, Outputs: &props},
						Status:     resource.StatusOK,
					}, nil
				},
			}, nil
		}),
	}
}

// replaceProgram registers resA with foo=*foo, delete-before-replace when *dbr.
func replaceProgram(foo *string, dbr *bool) deploytest.LanguageRuntimeFactory {
	return deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		d := *dbr
		_, err := monitor.RegisterResource("pkgA:m:typA", "resA", true, deploytest.ResourceOptions{
			Inputs:              resource.NewPropertyMapFromMap(map[string]any{"foo": *foo}),
			DeleteBeforeReplace: &d,
		})
		return err
	})
}

func replacementsAsked(v *replaceVetter) []deploy.DeleteCandidate {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []deploy.DeleteCandidate
	for _, c := range v.asked {
		if c.Replacement {
			out = append(out, c)
		}
	}
	return out
}

// A replace hands the original to the delete vet before the replacement's first step, once, in both replacement
// orders; a refusal stops the run before anything is created or deleted.
func TestPlungeDeleteVetterSeesTheDeleteHalfOfAReplace(t *testing.T) {
	t.Parallel()

	for _, dbr := range []bool{false, true} {
		for _, refuse := range []bool{false, true} {
			cloud := &replaceCloud{}
			foo, d := "a", dbr
			hostF := deploytest.NewPluginHostF(nil, nil, replaceProgram(&foo, &d), nil, nil, cloud.loaders()...)
			p := &lt.TestPlan{Options: lt.TestUpdateOptions{T: t, HostF: hostF}}
			project := p.GetProject()
			snap, err := lt.TestOp(Update).RunStep(project, p.GetTarget(t, nil), p.Options, false, p.BackendClient,
				nil, "0")
			require.NoError(t, err)

			foo = "b"
			vetter := &replaceVetter{refuse: refuse}
			target := p.GetTarget(t, snap)
			target.DeleteVetter = vetter
			_, err = lt.TestOp(Update).RunStep(project, target, p.Options, false, p.BackendClient, nil, "1")

			asked := replacementsAsked(vetter)
			require.Len(t, asked, 1, "dbr=%v refuse=%v: the original, asked once", dbr, refuse)
			assert.Equal(t, "resA", asked[0].State.URN.Name())
			assert.Equal(t, resource.ID("resA-1"), asked[0].State.ID)
			assert.NotNil(t, asked[0].Provider)
			creates, deletes, _ := cloud.counts()
			if refuse {
				require.Error(t, err)
				assert.Equal(t, 1, creates, "dbr=%v: no replacement is created", dbr)
				assert.Zero(t, deletes, "dbr=%v: the original is not deleted", dbr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 2, creates)
				assert.Equal(t, 1, deletes)
			}
		}
	}
}

// A pending delete an earlier run's replacement left (its delete failed) is asked about in the next run's deletes,
// as a replacement.
func TestPlungeDeleteVetterSeesALeftoverPendingDelete(t *testing.T) {
	t.Parallel()

	cloud := &replaceCloud{}
	foo, d := "a", false
	hostF := deploytest.NewPluginHostF(nil, nil, replaceProgram(&foo, &d), nil, nil, cloud.loaders()...)
	p := &lt.TestPlan{Options: lt.TestUpdateOptions{T: t, HostF: hostF}}
	project := p.GetProject()
	snap, err := lt.TestOp(Update).RunStep(project, p.GetTarget(t, nil), p.Options, false, p.BackendClient, nil, "0")
	require.NoError(t, err)

	// The replace creates resA-2 and fails to delete resA-1, which stays in the snapshot as a pending delete.
	foo = "b"
	cloud.failDeletes = true
	snap, err = lt.TestOp(Update).RunStep(project, p.GetTarget(t, snap), p.Options, false, p.BackendClient, nil, "1")
	require.Error(t, err)
	require.NotNil(t, snap)
	pending := false
	for _, res := range snap.Resources {
		pending = pending || (res.Delete && res.ID == "resA-1")
	}
	require.True(t, pending, "resA-1 is pending delete")

	cloud.mu.Lock()
	cloud.failDeletes = false
	cloud.mu.Unlock()
	vetter := &replaceVetter{refuse: true}
	target := p.GetTarget(t, snap)
	target.DeleteVetter = vetter
	_, err = lt.TestOp(Update).RunStep(project, target, p.Options, false, p.BackendClient, nil, "2")
	require.Error(t, err)

	asked := replacementsAsked(vetter)
	require.Len(t, asked, 1)
	assert.Equal(t, resource.ID("resA-1"), asked[0].State.ID)
	_, deletes, _ := cloud.counts()
	assert.Zero(t, deletes, "the refusal keeps the pending delete")
}

// An import is handed to the import vet before its step exists; a refusal stops the run before the import's read.
func TestPlungeImportVetterSeesImports(t *testing.T) {
	t.Parallel()

	for _, refuse := range []bool{false, true} {
		cloud := &replaceCloud{}
		programF := deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
			_, err := monitor.RegisterResource("pkgA:m:typA", "resA", true, deploytest.ResourceOptions{
				Inputs:   resource.NewPropertyMapFromMap(map[string]any{"foo": "a"}),
				ImportID: "live-1",
			})
			return err
		})
		hostF := deploytest.NewPluginHostF(nil, nil, programF, nil, nil, cloud.loaders()...)
		p := &lt.TestPlan{Options: lt.TestUpdateOptions{T: t, HostF: hostF}}
		vetter := &importVetter{refuse: refuse}
		target := p.GetTarget(t, nil)
		target.ImportVetter = vetter
		_, err := lt.TestOp(Update).RunStep(p.GetProject(), target, p.Options, false, p.BackendClient, nil, "0")

		require.Len(t, vetter.asked, 1, "refuse=%v", refuse)
		assert.Equal(t, "resA", vetter.asked[0].URN.Name())
		assert.Equal(t, resource.ID("live-1"), vetter.asked[0].ID)
		assert.NotNil(t, vetter.asked[0].Provider)
		_, _, reads := cloud.counts()
		if refuse {
			require.Error(t, err)
			assert.Zero(t, reads, "refused before the import step's read")
		} else {
			require.NoError(t, err)
			assert.Equal(t, 1, reads, "the import step's read")
		}
	}
}
