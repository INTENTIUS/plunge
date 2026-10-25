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

// Saved plans and record versions (INTENTIUS/plunge#2, decision 5).
//
// A Pulumi plan file (`up --save-plan`, `up --plan`) stores the input diff of each resource and no before-values,
// so a value another run moved after approval is not refused. plunge's answer is record versions: the plan carries
// the version of each record it was computed against, and applying it refuses, with exit code ExitPlanMoved, when
// any of those records has moved since. A record version moves exactly when a run wrote that record, so this is
// the record-store counterpart of choudoufu's before/after gate (exit 3 there too).
//
// What lives here is the backend half: taking the stamp, checking it, and the refusal. Carrying the stamp inside the
// plan file needs a field on apitype.DeploymentPlanV1 (sdk) and the load/save in pkg/cmd/pulumi/operations, which
// is P-L4's seam; until it lands, nothing in the CLI calls StampPlan or VerifyPlan.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/staterecord"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// ExitPlanMoved is the exit code for applying a saved plan whose records moved since it was approved.
const ExitPlanMoved = 3

// PlanRecordVersions is the version of each resource record a plan was computed against. A URN with no record
// maps to "", which asserts that the resource had no record when the plan was made.
type PlanRecordVersions map[resource.URN]string

// PlanMovedError refuses a saved plan whose records moved since it was approved. It names each record.
type PlanMovedError struct {
	Moved []MovedRecord
}

// MovedRecord is one record whose version is no longer the one a plan was approved against.
type MovedRecord struct {
	URN      resource.URN
	Approved string
	Now      string
}

func (e *PlanMovedError) Error() string {
	parts := make([]string, len(e.Moved))
	for i, m := range e.Moved {
		parts[i] = fmt.Sprintf("%s (approved against version %q, the store now holds %q)", m.URN, m.Approved, m.Now)
	}
	return "plunge: refusing the saved plan: these resources' records moved after it was approved, so it was " +
		"computed against values that are no longer recorded: " + strings.Join(parts, "; ")
}

// ExitCode is ExitPlanMoved.
func (e *PlanMovedError) ExitCode() int { return ExitPlanMoved }

// StampPlan returns the record version of each URN, as l read them.
func StampPlan(l *loaded, urns []resource.URN) PlanRecordVersions {
	out := PlanRecordVersions{}
	for _, urn := range urns {
		if k, ok := l.records[l.keys.resource(urn)]; ok {
			out[urn] = k.version
		} else {
			out[urn] = ""
		}
	}
	return out
}

// VerifyPlan re-reads each stamped record and refuses when any version moved.
func VerifyPlan(ctx context.Context, store staterecord.Store, keys stackKeys, stamp PlanRecordVersions) error {
	urns := make([]resource.URN, 0, len(stamp))
	for urn := range stamp {
		urns = append(urns, urn)
	}
	sort.Slice(urns, func(i, j int) bool { return urns[i] < urns[j] })

	var moved []MovedRecord
	for _, urn := range urns {
		_, version, _, err := store.Get(ctx, keys.resource(urn))
		if err != nil {
			return fmt.Errorf("plunge: reading the record of %s to check the saved plan: %w", urn, err)
		}
		if version != stamp[urn] {
			moved = append(moved, MovedRecord{URN: urn, Approved: stamp[urn], Now: version})
		}
	}
	if len(moved) > 0 {
		return &PlanMovedError{Moved: moved}
	}
	return nil
}
