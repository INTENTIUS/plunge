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

// Stack history, checkpoint backups and stack listings on the plunge backend (INTENTIUS/plunge#14).
//
// The DIY backend writes a history entry and a checkpoint backup on every save of its one checkpoint file. The
// plunge backend has no such file: a run writes the records whose content changed, and two runs may be writing one
// stack's records at the same time, so there is no single "update" for a history entry to describe. Left alone, the
// DIY backend's history for a plunge stack is empty, which reads as "this stack never ran". So history, a previous
// version's export and backups are refused by name instead (ruling on INTENTIUS/plunge#14, question 1).

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
)

// UnsupportedOnRecordsError refuses an operation the plunge backend does not keep the data for: stack history,
// exporting a previous version, and checkpoint backups.
type UnsupportedOnRecordsError struct {
	// What names the data: "stack history", "previous deployment versions", "checkpoint backups".
	What string
	Ref  string
}

func (e *UnsupportedOnRecordsError) Error() string {
	return fmt.Sprintf("plunge: the plunge backend keeps no %s for %s. Its state is one record per resource, "+
		"written by whichever run changed it, with no stack-wide update to record a history entry or a backup for. "+
		"Use `plunge stack export` for the current state; a record store with object versioning (S3, Azure Blob, "+
		"GCS) keeps each record's previous versions", e.What, e.Ref)
}

// GetHistory refuses by name: a plunge stack has no history (see the file comment).
func (b *plungeBackend) GetHistory(
	_ context.Context, ref backend.StackReference, _ int, _ int,
) ([]backend.UpdateInfo, error) {
	return nil, &UnsupportedOnRecordsError{What: "stack history", Ref: ref.String()}
}

// ExportDeploymentForVersion refuses by name, as GetHistory does: there are no numbered versions to export.
func (b *plungeBackend) ExportDeploymentForVersion(
	_ context.Context, s backend.Stack, _ string,
) (*apitype.UntypedDeployment, error) {
	return nil, &UnsupportedOnRecordsError{What: "previous deployment versions", Ref: s.Ref().String()}
}

var _ backend.SpecificDeploymentExporter = (*plungeBackend)(nil)

// recordStackSummary is a DIY stack summary whose resource count comes from the stack's records. The DIY
// checkpoint of a plunge stack is empty, so the DIY count is always nil or zero.
type recordStackSummary struct {
	backend.StackSummary
	count *int
}

func (s recordStackSummary) ResourceCount() *int { return s.count }

// ListStacks lists the DIY backend's stacks with each plunge stack's resource count read from its records: the
// number of resource states the assembled deployment holds, as the DIY backend counts its checkpoint's. A stack
// whose records cannot be read or assembled shows no count rather than failing the listing.
func (b *plungeBackend) ListStacks(
	ctx context.Context, filter backend.ListStacksFilter, token backend.ContinuationToken,
) ([]backend.StackSummary, backend.ContinuationToken, error) {
	summaries, next, err := b.Backend.ListStacks(ctx, filter, token)
	if err != nil {
		return nil, nil, err
	}
	out := make([]backend.StackSummary, len(summaries))
	for i, summary := range summaries {
		out[i] = recordStackSummary{StackSummary: summary, count: b.resourceCount(ctx, summary.Name())}
	}
	return out, next, nil
}

func (b *plungeBackend) resourceCount(ctx context.Context, ref backend.StackReference) *int {
	keys, err := keysFor(ref)
	if err != nil {
		return nil
	}
	l, err := load(ctx, b.store, keys)
	if err != nil {
		return nil
	}
	n := len(l.deployment.Deployment.Resources)
	return &n
}
