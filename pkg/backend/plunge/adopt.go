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

// Adopting a stock stack into plunge (INTENTIUS/plunge#7).
//
// A stock Pulumi stack on a DIY backend keeps one checkpoint. Pointing plunge at the same root (plunge+<diy url>)
// finds no records, and without a gate the first `up` would see an empty stack and create every resource again.
// So the gate is two-sided:
//
//   - Every operation on a stack with no records, whose DIY checkpoint at the same root holds resources, is refused
//     with AdoptionRequiredError, naming the command that adopts it.
//   - `plunge stack adopt` (pkg/cmd/pulumi/stack/stack_adopt.go) is that command. It writes nothing until the
//     operator confirms (Confirmed, the CLI's --yes or its prompt).
//
// Adoption takes the checkpoint's IDs as they are: every resource's state becomes its own record, unchanged, so the
// first plunge run diffs against exactly what stock Pulumi last recorded. Nothing is imported or read again. The
// markers land at that first `up`, as an in-place tag update on each taggable resource; that is the only change
// adoption makes to the cloud.
//
// It is recorded: the adoption record (stacks/<project>/<stack>/adoption) holds the checkpoint's digest, where it
// came from, and each resource's ownership as adoption classed it. A second adoption of the same checkpoint finds
// that record and writes nothing; a different checkpoint is refused.
//
// Refusals, each by name and before anything is written: a checkpoint with pending operations; a provider whose
// ignoreTags or defaultTags would hide the markers; and a resource whose tags are create-only (aws-native
// tagUpdatable: false), because marking it would replace it, and plunge never replaces to mark. Types plunge can
// only class with their provider's schema (the bridged providers) are classed at the first run, where the injector
// refuses a create-only one the same way (inject.go).
//
// Backing out: `plunge stack export` gives a deployment stock `pulumi stack import` takes. The checkpoint then
// holds the markers in each resource's inputs and on the live objects; stock `pulumi preview --refresh` shows one
// kind of change, the removal of the plunge-* tags, and `pulumi up` removes them.
//
// Identity: choudoufu's pkg/identity (required at the integration/choudoufu-engine commit 9807fdc1fa, as a
// pseudo-version in pkg/go.mod) is read for bridged types once their Terraform type
// is known, at the first run, and its claim is kept on each record (ownershipEntry.Identity), so an operator who backs
// out and re-imports has the import identity at hand. The checkpoint's provider ID is used as the ID throughout.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/pkg/v3/secrets"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	sdkproviders "github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// OwnershipPending is a resource adoption could not class without its provider's schema. The first run classes it.
const OwnershipPending Ownership = "pending"

// adoptionRecord is what the adoption key holds. It is created once (put-if-absent) and never rewritten.
type adoptionRecord struct {
	Format           string            `json:"format"`
	Source           string            `json:"source"`
	CheckpointDigest string            `json:"checkpointDigest"`
	AdoptedAt        string            `json:"adoptedAt"`
	Resources        []adoptedResource `json:"resources"`
}

// adoptedResource is one resource as adoption classed it.
type adoptedResource struct {
	URN       resource.URN `json:"urn"`
	Type      tokens.Type  `json:"type"`
	ID        resource.ID  `json:"id"`
	Ownership Ownership    `json:"ownership"`
	Reason    string       `json:"reason,omitempty"`
	// Marked reports that the checkpoint's inputs already carry this stack's markers.
	Marked bool `json:"marked,omitempty"`
}

// AdoptOptions are the inputs to an adoption.
type AdoptOptions struct {
	// Checkpoint is the stock deployment to adopt. Nil reads the DIY checkpoint at the plunge backend's own root.
	Checkpoint *apitype.UntypedDeployment
	// Source names where Checkpoint came from, for the record. Empty names the DIY checkpoint.
	Source string
	// Confirmed is the gate: without it, Adopt classifies and refuses, and writes nothing.
	Confirmed bool
	// SecretsProvider decrypts the checkpoint's secrets.
	SecretsProvider secrets.Provider
}

// AdoptResult is what an adoption did, or would do.
type AdoptResult struct {
	// AlreadyAdopted reports that the stack's adoption record names this checkpoint, so nothing was written.
	AlreadyAdopted bool
	// Written reports that records were written by this call.
	Written   bool
	Digest    string
	Resources []AdoptedResource
}

// AdoptedResource is one resource in an AdoptResult.
type AdoptedResource struct {
	URN       resource.URN
	ID        resource.ID
	Ownership Ownership
	Reason    string
}

// Adopter is implemented by the plunge backend. The CLI reaches adoption through it.
type Adopter interface {
	Adopt(ctx context.Context, s backend.Stack, opts AdoptOptions) (*AdoptResult, error)
}

var _ Adopter = (*plungeBackend)(nil)

// AdoptionRequiredError refuses an operation on a stack that has a stock checkpoint and no plunge records.
type AdoptionRequiredError struct {
	Stack     string
	Resources int
}

func (e *AdoptionRequiredError) Error() string {
	return fmt.Sprintf("plunge: the stack %s has a stock Pulumi checkpoint holding %d resources and no plunge records; "+
		"running against it now would see an empty stack and create everything again. Adopt it first with "+
		"`plunge stack adopt --stack %s`", e.Stack, e.Resources, e.Stack)
}

// AdoptRefusedError refuses an adoption before anything is written, naming every reason.
type AdoptRefusedError struct {
	Stack   string
	Reasons []string
}

func (e *AdoptRefusedError) Error() string {
	return fmt.Sprintf("plunge: refusing to adopt %s; nothing was written:\n  - %s", e.Stack,
		strings.Join(e.Reasons, "\n  - "))
}

// AdoptUnconfirmedError is the gate: an adoption that was classified and would write, but was not confirmed.
type AdoptUnconfirmedError struct {
	Stack  string
	Result *AdoptResult
}

func (e *AdoptUnconfirmedError) Error() string {
	return fmt.Sprintf("plunge: adopting %s writes %d resource records and the adoption record; nothing was "+
		"written because the adoption was not confirmed (pass --yes)", e.Stack, len(e.Result.Resources))
}

// checkpointDigest is the digest an adoption record holds: of the deployment, in canonical JSON.
func checkpointDigest(d *apitype.UntypedDeployment) (string, error) {
	canon, err := canonicalJSON(d.Deployment)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}

// Adopt implements Adopter.
func (b *plungeBackend) Adopt(ctx context.Context, s backend.Stack, opts AdoptOptions) (*AdoptResult, error) {
	ref := s.Ref()
	keys, err := keysFor(ref)
	if err != nil {
		return nil, err
	}
	l, err := load(ctx, b.store, keys)
	if err != nil {
		return nil, err
	}

	d, source := opts.Checkpoint, opts.Source
	if d == nil {
		if d, err = b.Backend.ExportDeployment(ctx, unwrap(s)); err != nil {
			return nil, fmt.Errorf("plunge: reading the stock checkpoint of %s: %w", ref, err)
		}
		source = "the DIY checkpoint at " + b.Backend.URL()
	}
	digest, err := checkpointDigest(d)
	if err != nil {
		return nil, fmt.Errorf("plunge: reading the stock checkpoint of %s: %w", ref, err)
	}

	if l.adoption != nil {
		if l.adoption.CheckpointDigest == digest {
			res := &AdoptResult{AlreadyAdopted: true, Digest: digest}
			for _, r := range l.adoption.Resources {
				res.Resources = append(res.Resources, AdoptedResource{URN: r.URN, ID: r.ID, Ownership: r.Ownership,
					Reason: r.Reason})
			}
			return res, nil
		}
		return nil, &AdoptRefusedError{Stack: ref.String(), Reasons: []string{fmt.Sprintf(
			"the stack was already adopted from another checkpoint (%s, digest %s, at %s); this one has digest %s",
			l.adoption.Source, l.adoption.CheckpointDigest, l.adoption.AdoptedAt, digest)}}
	}

	v3, err := stack.UnmarshalUntypedDeployment(ctx, d)
	if err != nil {
		return nil, err
	}
	rec, reasons := b.classify(ctx, v3)
	rec.Format, rec.Source, rec.CheckpointDigest = adoptionFormat, source, digest

	// Records already present: only a resumed adoption of this same checkpoint (records written, the adoption
	// record not) is finished; anything else is someone else's state.
	hasRecords := false
	for _, k := range l.records {
		if k.resource != nil {
			hasRecords = true
			break
		}
	}
	resume := false
	if hasRecords {
		if sameResources(l.deployment.Deployment.Resources, v3.Resources) {
			resume = true
		} else {
			reasons = append(reasons, "the stack already has plunge records that are not this checkpoint's; adopt "+
				"only a stack plunge has never run against")
		}
	}
	if len(v3.Resources) == 0 {
		reasons = append(reasons, "the checkpoint holds no resources; a new stack needs no adoption")
	}
	if len(reasons) > 0 {
		return nil, &AdoptRefusedError{Stack: ref.String(), Reasons: reasons}
	}

	res := &AdoptResult{Digest: digest}
	for _, r := range rec.Resources {
		res.Resources = append(res.Resources, AdoptedResource{URN: r.URN, ID: r.ID, Ownership: r.Ownership,
			Reason: r.Reason})
	}
	if !opts.Confirmed {
		return nil, &AdoptUnconfirmedError{Stack: ref.String(), Result: res}
	}

	if !resume {
		led := newLedger()
		for _, r := range rec.Resources {
			if r.Ownership == OwnershipRecord {
				r := r
				led.record(r.URN, func(e *ownershipEntry) {
					e.By, e.State, e.Family, e.Reason = OwnershipRecord, MarkerUntaggable, familyOf(r.Type), r.Reason
				})
			}
		}
		p := newRecordPersister(ctx, b.store, l, Generation{}, false, nil)
		p.ownership = led
		if err := p.Save(apitype.TypedDeployment{Deployment: v3, Version: d.Version, Features: d.Features}); err != nil {
			return nil, err
		}
	}
	rec.AdoptedAt = time.Now().UTC().Format(time.RFC3339)
	payload, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	if _, err := b.store.PutIfAbsent(ctx, keys.adoption(), payload); err != nil {
		return nil, fmt.Errorf("plunge: writing the adoption record at %q: %w", keys.adoption(), err)
	}
	res.Written = true
	return res, nil
}

// classify classes every resource of a stock deployment for adoption, offline, and returns the reasons it refuses.
func (b *plungeBackend) classify(ctx context.Context, d *apitype.DeploymentV3) (*adoptionRecord, []string) {
	rec := &adoptionRecord{}
	var reasons []string
	if len(d.PendingOperations) > 0 {
		urns := make([]string, len(d.PendingOperations))
		for i, op := range d.PendingOperations {
			urns[i] = fmt.Sprintf("%s (%s)", op.Resource.URN, op.Type)
		}
		reasons = append(reasons, "the checkpoint has pending operations, which stock Pulumi must settle first "+
			"(`pulumi refresh` or `pulumi stack export`/`import` after checking the cloud): "+strings.Join(urns, ", "))
	}

	providerVersions := map[string]string{} // provider reference -> version input
	for _, r := range d.Resources {
		if r.Delete || !sdkproviders.IsProviderType(r.Type) {
			continue
		}
		inputs := resource.NewPropertyMapFromMap(r.Inputs)
		if err := checkProviderInputs(r.URN, inputs); err != nil {
			reasons = append(reasons, err.Error())
		}
		providerVersions[string(r.URN)+resource.URNNameDelimiter+string(r.ID)] = providerString(inputs, "version")
	}

	cat := newCatalog()
	for _, r := range d.Resources {
		if r.Delete {
			continue
		}
		if !r.Custom || sdkproviders.IsProviderType(r.Type) {
			continue
		}
		info := TypeInfo{Type: r.Type, Family: familyOf(r.Type)}
		entry := adoptedResource{URN: r.URN, Type: r.Type, ID: r.ID, Ownership: OwnershipPending}
		switch info.Family {
		case FamilyAWSNative:
			version := providerVersions[r.Provider]
			if version == "" {
				entry.Reason = "the aws-native provider version is not in the checkpoint; classed at the first run"
				break
			}
			md, err := cat.awsNative.Load(ctx, version)
			if err != nil {
				entry.Reason = err.Error()
				break
			}
			nativeInfo(md, &info)
		case FamilyKubernetes:
			kubernetesInfo(&info)
		default:
			entry.Reason = "classed at the first run, from the provider's schema"
		}
		inputs := resource.NewPropertyMapFromMap(r.Inputs)
		if info.Shape == ShapeMap || info.Shape == ShapeKeyValueArray {
			want, err := markers.For(r.URN, info.Encoding())
			if err != nil {
				reasons = append(reasons, err.Error())
				continue
			}
			entry.Marked = carries(inputs, info, want)
			entry.Ownership, entry.Reason = OwnershipMarker, ""
			if info.CreateOnly && !entry.Marked {
				reasons = append(reasons, (&CreateOnlyTagsError{URN: r.URN, Type: r.Type, Property: info.Property}).Error())
			}
		} else if info.Shape == ShapeNone {
			entry.Ownership, entry.Reason = OwnershipRecord, info.Reason
		} else if info.Shape == ShapeUnsupported {
			entry.Ownership, entry.Reason = OwnershipRecord, info.Reason
		}
		rec.Resources = append(rec.Resources, entry)
	}
	sort.Slice(rec.Resources, func(i, j int) bool { return rec.Resources[i].URN < rec.Resources[j].URN })
	return rec, reasons
}

// sameResources reports whether two deployments hold the same live resources, by URN, type and ID.
func sameResources(a, b []apitype.ResourceV3) bool {
	set := func(rs []apitype.ResourceV3) map[string]bool {
		out := map[string]bool{}
		for _, r := range rs {
			if !r.Delete {
				out[string(r.URN)+"|"+string(r.Type)+"|"+string(r.ID)] = true
			}
		}
		return out
	}
	sa, sb := set(a), set(b)
	if len(sa) != len(sb) {
		return false
	}
	for k := range sa {
		if !sb[k] {
			return false
		}
	}
	return true
}

// requireAdopted is the gate apply runs: a stack with no records whose DIY checkpoint holds resources is refused.
func (b *plungeBackend) requireAdopted(ctx context.Context, s backend.Stack, l *loaded, sp secrets.Provider) error {
	for _, k := range l.records {
		if k.resource != nil || k.adoption != nil {
			return nil
		}
	}
	snap, err := unwrap(s).Snapshot(ctx, sp)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		// An unreadable stock checkpoint is not proof there is none; refuse rather than create everything.
		return fmt.Errorf("plunge: the stack %s has no plunge records, and its DIY checkpoint could not be read to "+
			"check whether it needs adopting: %w", s.Ref(), err)
	}
	if snap == nil {
		return nil
	}
	n := 0
	for _, r := range snap.Resources {
		if r.Custom && !sdkproviders.IsProviderType(r.Type) && !r.Delete {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return &AdoptionRequiredError{Stack: s.Ref().String(), Resources: n}
}
