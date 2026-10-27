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


package plunge

import (
	"time"

	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
)

// Per-resource timings in `plunge up --json` (INTENTIUS/plunge#55). apply gives an update that writes records a
// deploy.Timings on its target: the engine times each resource's provider calls through it, the persister adds each
// resource record's write or delete below, and the engine puts them on the resource's resource-outputs or
// resource-operation-failed event as metadata.plungeTimings. A write conditioned on a version that lost to another
// run is reported as failed even when the persister then adopts the other run's equal record.

// timeRecord adds one record-store call for the resource record r, if this run records timings. Output, snippet,
// extension and secrets records belong to no resource step and are not timed.
func (p *recordPersister) timeRecord(r *resourceRecord, call string, start time.Time, err error) {
	if p.timings == nil || r == nil {
		return
	}
	p.timings.Record(r.URN, apitype.PlungeTimingRecord, call, start, time.Since(start), err != nil)
}
