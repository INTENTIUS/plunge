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
// the record-store counterpart of choudoufu's before/after gate (which exits 3 for it).
//
// What lives here is the backend half: taking the stamp, checking it, and the refusal. The stamp rides in the plan
// file as apitype.DeploymentPlanV1.RecordVersions (P-L4, INTENTIUS/plunge#7): `preview --save-plan` stamps it
// through PlanStamper (pkg/cmd/pulumi/operations/preview.go), and `up --plan` verifies it before the update runs
// (up.go). A plan with no stamp is refused on this backend, since nothing then says what it was approved against.
//
// The stamp is the versions the preview itself read, not a later re-read, so a write that lands between the preview
// and the save is caught too.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// ExitPlanMoved is the exit code for applying a saved plan whose records moved since it was approved
// (INTENTIUS/plunge#11). Upstream's taxonomy (pkg/cmd/pulumi/cmd/exit_codes.go) grows one code at a time from 0,
// and its 3 is ExitAuthenticationError, so plunge's refusals take a code well above it and below the sysexits
// range (64-78). choudoufu and chant give a moved plan 3; a driver reading plunge's status maps 40 itself.
const ExitPlanMoved = 40

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

// CustomExitCode is ExitPlanMoved; the CLI's exit-code mapping (pkg/cmd/pulumi/cmd/exit_codes.go) reads it.
func (e *PlanMovedError) CustomExitCode() int { return ExitPlanMoved }

// UnstampedPlanError refuses a saved plan with no record-version stamp on the plunge backend.
type UnstampedPlanError struct {
	Path string
}

func (e *UnstampedPlanError) Error() string {
	return fmt.Sprintf("plunge: the plan %s carries no record versions, so nothing says which records it was "+
		"approved against; save it again with `plunge preview --save-plan` against this backend", e.Path)
}

// CustomExitCode is ExitPlanMoved: an unstamped plan is refused for the same reason a moved one is.
func (e *UnstampedPlanError) CustomExitCode() int { return ExitPlanMoved }

// PlanStamper is the plunge backend's saved-plan stamp, as the CLI reaches it.
type PlanStamper interface {
	// StampPlan returns the record version of each URN, as the stack's last preview in this process read them
	// (or as the records are now, when there was none).
	StampPlan(ctx context.Context, s backend.Stack, urns []resource.URN) (PlanRecordVersions, error)
	// VerifyPlan refuses with *PlanMovedError when any stamped record has moved.
	VerifyPlan(ctx context.Context, s backend.Stack, stamp PlanRecordVersions) error
}

var _ PlanStamper = (*plungeBackend)(nil)

func (b *plungeBackend) StampPlan(
	ctx context.Context, s backend.Stack, urns []resource.URN,
) (PlanRecordVersions, error) {
	keys, err := keysFor(s.Ref())
	if err != nil {
		return nil, err
	}
	l, ok := b.previewed(s.Ref().String())
	if !ok {
		if l, err = load(ctx, b.store, keys); err != nil {
			return nil, err
		}
	}
	return StampPlan(l, urns), nil
}

func (b *plungeBackend) VerifyPlan(ctx context.Context, s backend.Stack, stamp PlanRecordVersions) error {
	keys, err := keysFor(s.Ref())
	if err != nil {
		return err
	}
	return VerifyPlan(ctx, b.store, keys, stamp)
}

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
