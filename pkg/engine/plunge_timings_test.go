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

package engine

import (
	"errors"
	"io"
	"testing"
	"time"

	pkgresource "github.com/pulumi/pulumi/pkg/v3/resource"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/util/cancel"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingMutation stands in for the plunge persister: End records a record write for the step's resource, as
// the record persister does when the snapshot manager saves.
type recordingMutation struct {
	timings *deploy.Timings
	ended   bool
}

func (m *recordingMutation) End(step deploy.Step, successful bool) error {
	m.ended = true
	m.timings.Record(step.URN(), apitype.PlungeTimingRecord, "write", time.Now(), time.Millisecond, false)
	return nil
}

// A partly failed update's failure event keeps the step's new inputs on the plunge timings path, as stock backends
// do, although OnResourceStepPost resets them to the old inputs before the deferred event is sent, and the event
// carries the record write End made (INTENTIUS/plunge#55).
func TestPlungeTimingsPartialFailureKeepsNewInputs(t *testing.T) {
	t.Parallel()

	urn := resource.URN("urn:pulumi:stack::project::my:example:Foo::foo")
	timings := deploy.NewTimings()
	timings.Record(urn, apitype.PlungeTimingProvider, "update", time.Now(), 2*time.Millisecond, true)
	dep := deploy.NewTimedDeploymentForTest(timings)

	old := &pkgresource.State{
		URN: urn, Type: urn.Type(), ID: "foo", Custom: true, Provider: "unimportant",
		Inputs: resource.PropertyMap{"size": resource.NewProperty("old")},
	}
	updated := &pkgresource.State{
		URN: urn, Type: urn.Type(), Custom: true, Provider: "unimportant",
		Inputs: resource.PropertyMap{"size": resource.NewProperty("new")},
	}
	step := deploy.NewUpdateStep(dep, nil, old, updated, nil, nil, nil, nil, nil)

	cancelCtx, _ := cancel.NewContext(t.Context())
	sink := diag.DefaultSink(io.Discard, io.Discard, diag.FormatOptions{Color: colors.Never})
	acts := newUpdateActions(&Context{Cancel: cancelCtx}, UpdateInfo{}, &deploymentOptions{Diag: sink})
	eventsChan := make(chan Event, 10)
	acts.Opts.Events.ch = eventsChan
	acts.Seen[urn] = step

	mutation := &recordingMutation{timings: timings}
	err := acts.OnResourceStepPost(mutation, step, resource.StatusPartialFailure, errors.New("update failed"))
	require.NoError(t, err)
	require.True(t, mutation.ended)

	e := <-eventsChan
	require.Equal(t, ResourceOperationFailed, e.Type)
	p, ok := e.Payload().(ResourceOperationFailedPayload)
	require.True(t, ok)
	require.NotNil(t, p.Metadata.New)
	assert.Equal(t, resource.NewProperty("new"), p.Metadata.New.Inputs["size"])
	assert.Equal(t, resource.NewProperty("old"), updated.Inputs["size"], "the engine still persists the old inputs")

	require.Len(t, p.Metadata.PlungeTimings, 2)
	assert.Equal(t, "update", p.Metadata.PlungeTimings[0].Call)
	assert.Equal(t, apitype.PlungeTimingRecord, p.Metadata.PlungeTimings[1].Kind)
	assert.Equal(t, "write", p.Metadata.PlungeTimings[1].Call)
}
