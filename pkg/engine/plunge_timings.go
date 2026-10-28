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
	"github.com/pulumi/pulumi/pkg/v3/display"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
)

// plunge's per-resource timings (INTENTIUS/plunge#55). The plunge record backend sets deploy.Target.Timings; every
// other backend leaves it nil, and these helpers then send the step's event at once, exactly as upstream does.
//
// When the deployment records timings, the event's payload is built at the point upstream sends it, so it shows
// the step as it was then (a partly failed update's new inputs included, before OnResourceStepPost resets them to
// the old ones). The returned func, which OnResourceStepPost defers, adds the timings recorded by then and sends it.
// OnResourceStepPost ends the snapshot mutation in its return statement, which runs before its deferred calls, so
// the event carries the record write that settled the step.

// outputsAfterRecordWrite is resourceOutputsEvent, sent after the step's record write when timings are recorded.
func (e *eventEmitter) outputsAfterRecordWrite(
	op display.StepOp, step deploy.Step, planning, debug, internal, showSecrets bool,
) func() {
	if deploy.StepTimings(step) == nil {
		e.resourceOutputsEvent(op, step, planning, debug, internal, showSecrets)
		return func() {}
	}
	contract.Requiref(e != nil, "e", "!= nil")
	payload := ResourceOutputsEventPayload{
		Metadata: makeStepEventMetadata(op, step, debug, showSecrets),
		Planning: planning,
		Debug:    debug,
		Internal: internal,
	}
	return func() {
		payload.Metadata = withPlungeTimings(payload.Metadata, step)
		e.sendEvent(NewEvent(payload))
	}
}

// operationFailedAfterRecordWrite is resourceOperationFailedEvent, sent after the step's record write when timings
// are recorded.
func (e *eventEmitter) operationFailedAfterRecordWrite(
	step deploy.Step, status resource.Status, steps int32, awaited, debug, showSecrets bool,
) func() {
	if deploy.StepTimings(step) == nil {
		e.resourceOperationFailedEvent(step, status, steps, awaited, debug, showSecrets)
		return func() {}
	}
	contract.Requiref(e != nil, "e", "!= nil")
	payload := ResourceOperationFailedPayload{
		Metadata: makeStepEventMetadata(step.Op(), step, debug, showSecrets),
		Status:   status,
		Steps:    steps,
		Awaited:  awaited,
	}
	return func() {
		payload.Metadata = withPlungeTimings(payload.Metadata, step)
		e.sendEvent(NewEvent(payload))
	}
}

// withPlungeTimings moves the calls recorded for step's resource onto md.
func withPlungeTimings(md StepEventMetadata, step deploy.Step) StepEventMetadata {
	if t := deploy.StepTimings(step); t != nil {
		md.PlungeTimings = t.Take(step.URN())
	}
	return md
}
