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

// outputsRecordingManager stands in for the plunge persister on a component's or the stack's
// RegisterResourceOutputs: the snapshot write it triggers records a record write for the resource.
type outputsRecordingManager struct {
	SnapshotManager
	timings *deploy.Timings
}

func (m *outputsRecordingManager) RegisterResourceOutputs(step deploy.Step) error {
	m.timings.Record(step.URN(), apitype.PlungeTimingRecord, "write", time.Now(), time.Millisecond, false)
	return nil
}

// fakeReg is the registration a create step requires; these tests never call it.
type fakeReg struct{ deploy.RegisterResourceEvent }

func newTimingsActions(t *testing.T, timings *deploy.Timings) (*updateActions, chan Event) {
	cancelCtx, _ := cancel.NewContext(t.Context())
	sink := diag.DefaultSink(io.Discard, io.Discard, diag.FormatOptions{Color: colors.Never})
	acts := newUpdateActions(&Context{
		Cancel:          cancelCtx,
		SnapshotManager: &outputsRecordingManager{timings: timings},
	}, UpdateInfo{}, &deploymentOptions{Diag: sink})
	events := make(chan Event, 10)
	acts.Opts.Events.ch = events
	return acts, events
}

func takeOutputsEvent(t *testing.T, events chan Event) ResourceOutputsEventPayload {
	t.Helper()
	select {
	case e := <-events:
		require.Equal(t, ResourceOutputsEvent, e.Type)
		p, ok := e.Payload().(ResourceOutputsEventPayload)
		require.True(t, ok)
		return p
	default:
		require.FailNow(t, "no event was sent")
		return ResourceOutputsEventPayload{}
	}
}

// A component's and the stack's outputs event is sent after the record write RegisterResourceOutputs makes, and
// carries it and the write made when its create step settled, which had no event of its own (the corpus aws-ts
// `docs` component and stack resource, INTENTIUS/plunge#55).
func TestPlungeTimingsComponentOutputsEventCarriesRecordWrites(t *testing.T) {
	t.Parallel()

	for _, urn := range []resource.URN{
		"urn:pulumi:floci::corpus-aws-ts::corpus:index:Site::docs",
		"urn:pulumi:floci::corpus-aws-ts::pulumi:pulumi:Stack::corpus-aws-ts-floci",
	} {
		timings := deploy.NewTimings()
		dep := deploy.NewTimedDeploymentForTest(timings)
		state := &pkgresource.State{URN: urn, Type: urn.Type()}
		step := deploy.NewCreateStep(dep, fakeReg{}, state)

		acts, events := newTimingsActions(t, timings)
		acts.Seen[urn] = step
		// The create step's record write, made when the step settled.
		timings.Record(urn, apitype.PlungeTimingRecord, "write", time.Now(), time.Millisecond, false)

		require.NoError(t, acts.OnResourceOutputs(step))
		p := takeOutputsEvent(t, events)
		require.Len(t, p.Metadata.PlungeTimings, 2, "%s", urn)
		for _, x := range p.Metadata.PlungeTimings {
			assert.Equal(t, apitype.PlungeTimingRecord, x.Kind)
			assert.Equal(t, "write", x.Call)
			assert.Empty(t, x.URN, "the event's own resource's calls name no other URN")
		}
	}
}

// A default provider's step event is internal, and `up --json` does not show it, so its record write is left for
// the first event of a resource that uses the provider, which carries it with the provider's URN. The provider's
// own event carries nothing, and no later event carries the write again.
func TestPlungeTimingsDefaultProviderWriteRidesOnFirstUser(t *testing.T) {
	t.Parallel()

	provURN := resource.URN("urn:pulumi:floci::corpus-aws-ts::pulumi:providers:aws::default_7_51_1")
	provRef := string(provURN) + "::b96b315f-e03c-5c4c-ac27-ff3cf46e44dd"
	timings := deploy.NewTimings()
	dep := deploy.NewTimedDeploymentForTest(timings)
	acts, events := newTimingsActions(t, timings)

	provState := &pkgresource.State{URN: provURN, Type: provURN.Type(), Custom: true}
	provStep := deploy.NewCreateStep(dep, fakeReg{}, provState)
	acts.Seen[provURN] = provStep
	require.NoError(t, acts.OnResourceStepPost(&recordingMutation{timings: timings}, provStep, resource.StatusOK, nil))
	p := takeOutputsEvent(t, events)
	assert.True(t, p.Internal)
	assert.Empty(t, p.Metadata.PlungeTimings, "the internal event took the provider's write")

	user := func(name string) []apitype.PlungeTiming {
		urn := resource.URN("urn:pulumi:floci::corpus-aws-ts::aws:sqs/queue:Queue::" + name)
		state := &pkgresource.State{URN: urn, Type: urn.Type(), Custom: true, Provider: provRef}
		step := deploy.NewCreateStep(dep, fakeReg{}, state)
		acts.Seen[urn] = step
		timings.Record(urn, apitype.PlungeTimingProvider, "create", time.Now(), time.Millisecond, false)
		require.NoError(t, acts.OnResourceStepPost(&recordingMutation{timings: timings}, step, resource.StatusOK, nil))
		return takeOutputsEvent(t, events).Metadata.PlungeTimings
	}

	first := user("queue-a")
	require.Len(t, first, 3)
	assert.Equal(t, provURN, first[0].URN)
	assert.Equal(t, apitype.PlungeTimingRecord, first[0].Kind)
	assert.Equal(t, "write", first[0].Call)
	assert.Equal(t, "create", first[1].Call)
	assert.Empty(t, first[1].URN)
	assert.Equal(t, "write", first[2].Call)
	assert.Empty(t, first[2].URN)

	second := user("queue-b")
	require.Len(t, second, 2, "the provider's write is carried once")
	for _, x := range second {
		assert.Empty(t, x.URN)
	}
}
