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

// Tests for the delete-vet seam the plunge record backend sets on deploy.Target (INTENTIUS/plunge#37): every
// delete, a destroy's included, is handed to Target.DeleteVetter before any delete step exists, and a refusal
// stops the run. Written for plunge#37, not run in that unit.

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

// recordingVetter records what it is asked about and refuses when refuse is set.
type recordingVetter struct {
	mu     sync.Mutex
	asked  []resource.URN
	refuse bool
}

func (v *recordingVetter) VetDeletes(_ context.Context, req deploy.DeleteVetRequest) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, c := range req.Deletes {
		v.asked = append(v.asked, c.State.URN)
	}
	if v.refuse {
		return errors.New("the live marker names another stack")
	}
	return nil
}

func TestPlungeDeleteVetterSeesDestroyAndUpDeletes(t *testing.T) {
	t.Parallel()

	for _, op := range []lt.TestOp{lt.TestOp(Update), lt.TestOp(Destroy)} {
		for _, refuse := range []bool{false, true} {
			var mu sync.Mutex
			deleted := 0
			loaders := []*deploytest.ProviderLoader{
				deploytest.NewProviderLoader("pkgA", semver.MustParse("1.0.0"), func() (plugin.Provider, error) {
					return &deploytest.Provider{
						DeleteF: func(context.Context, plugin.DeleteRequest) (plugin.DeleteResponse, error) {
							mu.Lock()
							defer mu.Unlock()
							deleted++
							return plugin.DeleteResponse{}, nil
						},
					}, nil
				}),
			}
			registered := true
			programF := deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
				if registered {
					_, err := monitor.RegisterResource("pkgA:m:typA", "resA", true)
					return err
				}
				return nil
			})
			hostF := deploytest.NewPluginHostF(nil, nil, programF, nil, nil, loaders...)
			p := &lt.TestPlan{Options: lt.TestUpdateOptions{T: t, HostF: hostF}}
			project := p.GetProject()

			snap, err := lt.TestOp(Update).RunStep(project, p.GetTarget(t, nil), p.Options, false, p.BackendClient,
				nil, "0")
			require.NoError(t, err)

			registered = false
			vetter := &recordingVetter{refuse: refuse}
			target := p.GetTarget(t, snap)
			target.DeleteVetter = vetter
			_, err = op.RunStep(project, target, p.Options, false, p.BackendClient, nil, "1")

			require.Len(t, vetter.asked, 1, "only resA is asked about, not its provider")
			assert.Equal(t, "resA", vetter.asked[0].Name())
			if refuse {
				require.Error(t, err)
				assert.Zero(t, deleted, "a refusal comes before any delete")
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, deleted)
			}
		}
	}
}
