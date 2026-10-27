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

import "github.com/pulumi/pulumi/pkg/v3/resource/deploy"

// plunge's per-resource timings (INTENTIUS/plunge#55). The plunge record backend sets deploy.Target.Timings; every
// other backend leaves it nil, and these helpers then leave events and their order exactly as upstream has them.

// withPlungeTimings moves the calls recorded for step's resource onto md.
func withPlungeTimings(md StepEventMetadata, step deploy.Step) StepEventMetadata {
	if t := deploy.StepTimings(step); t != nil {
		md.PlungeTimings = t.Take(step.URN())
	}
	return md
}

// afterRecordWrite runs emit now when step's deployment records no timings. When it does, it returns emit for the
// caller to defer, so the step's event is sent after the snapshot mutation has written the step's record and the
// event can carry that write's duration. OnResourceStepPost ends the mutation in its return statement, which runs
// before its deferred calls.
func afterRecordWrite(step deploy.Step, emit func()) func() {
	if deploy.StepTimings(step) == nil {
		emit()
		return func() {}
	}
	return emit
}
