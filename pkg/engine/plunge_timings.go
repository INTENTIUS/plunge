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
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/providers"
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
// the event carries the record write that settled the step. OnResourceOutputs, which sends a component's and the
// stack's outputs event, does the same with the write RegisterResourceOutputs makes; that event also carries the
// write made when the component's step settled, since a component's step sends no event of its own.

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

// outputsAfterRegisterOutputs is OnResourceOutputs's resourceOutputsEvent, sent after the record write
// RegisterResourceOutputs makes when timings are recorded, and at once, as upstream sends it, otherwise.
func (acts *updateActions) outputsAfterRegisterOutputs(step deploy.Step) func() {
	return acts.Opts.Events.outputsAfterRecordWrite(
		step.Op(), step, false /*planning*/, acts.Opts.Debug, isInternalStep(step), acts.Opts.ShowSecrets)
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
//
// A default provider's step event is internal, and `up --json` does not show it, so its calls are left in the
// recorder. The first event of a resource that uses the provider carries them, ahead of its own and with the
// provider's URN on each, and Take makes sure no later event carries them again.
func withPlungeTimings(md StepEventMetadata, step deploy.Step) StepEventMetadata {
	t := deploy.StepTimings(step)
	if t == nil || isDefaultProviderStep(step) {
		return md
	}
	var carried []apitype.PlungeTiming
	if res := step.Res(); res != nil && res.Provider != "" {
		if ref, err := providers.ParseReference(res.Provider); err == nil && providers.IsDefaultProvider(ref.URN()) {
			carried = t.Take(ref.URN())
			for i := range carried {
				carried[i].URN = ref.URN()
			}
		}
	}
	if own := t.Take(step.URN()); len(carried) > 0 || len(own) > 0 {
		md.PlungeTimings = append(carried, own...)
	}
	return md
}
