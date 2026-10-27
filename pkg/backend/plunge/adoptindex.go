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

// Adopting indexed objects from an adoption file (INTENTIUS/plunge#52; design and ruling on choudoufu#2284).
//
// `plunge stack adopt --adopt FILE` reads an `adopt: v1` adoption file (choudoufu pkg/adoption, chant#4536's
// schema), resolved against the index version it names. The index proposes and the live read decides: for each
// selected node, before anything is written,
//
//   - the engine type is the selection's `type`, or the one Pulumi type the node's inventory type joins to
//     (aws-native from a CloudFormation type, kubernetes from a group/version/kind). Every other family needs the
//     selection to name its type. A pulumi-aws type must be one choudoufu's registry joins to the node's
//     CloudFormation type;
//   - the stack's own default provider for the type's package is loaded and configured from the stack's
//     configuration, as `plunge up` would configure it, so every read goes where the stack's provider goes. A
//     provider configuration whose ignoreTags or defaultTags would hide the markers is refused;
//   - a type whose tags can only be set at create is refused by name, before any provider call (plunge never
//     replaces to mark), as plunge#51 refuses such an `import:`;
//   - the import ID is the node's Cloud Control identifier for aws-native, the Terraform id choudoufu's
//     pkg/identity translates it to for pulumi-aws (CloudControlTranslation, choudoufu#2286; a translation that
//     reads the model reads it through Cloud Control on the stack's endpoint, and one that needs the account ID
//     reads it with STS on the stack's endpoint, both under plunge#27's rules), the ARM id as returned for Azure,
//     GoogleAssetImportID for Google, and namespace/name for Kubernetes. A type pkg/identity classes as not
//     importable is refused;
//   - the object is read once through the provider's Read, the call the import step makes. A live object carrying
//     another stack's marker, or this stack's marker for another resource, is refused (plunge#51, plunge#37); one
//     the read does not find is refused; one already carrying this resource's markers is adopted as marked.
//
// What is written, after the gate (Confirmed: the CLI's --yes or its prompt): a record per adopted resource, built
// from the Read's inputs and outputs, with the default provider's record and the root stack's, and the adoption
// record naming the adoption file's index version and digest. No marker is written by adoption. The markers land
// at the first `plunge up`, as an in-place tags update the injector adds and up's preview and confirmation show:
// that is the second half of the gate (choudoufu#2284 Q3). The adoption view lists each marker write as tags only.
// Record-owned types (no tags argument, an unsupported shape, markerless per pkg/identity) get an ownership entry
// saying the record alone carries their ownership.
//
// An index adoption is a stack's one adoption, like a checkpoint's: the stack must have no records (or only this
// adoption's, written by an adoption that stopped before its adoption record) and no stock checkpoint holding
// resources; adopting the same file again writes nothing.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver"
	"github.com/google/uuid"

	"github.com/intentius/choudoufu/pkg/adoption"
	"github.com/intentius/choudoufu/pkg/cloudcontrol"
	"github.com/intentius/choudoufu/pkg/identity"
	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	pkgresource "github.com/pulumi/pulumi/pkg/v3/resource"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	deployproviders "github.com/pulumi/pulumi/pkg/v3/resource/deploy/providers"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/pkg/v3/secrets"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/env"
	sdkproviders "github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
)

// MarkerAdoptedPending is the ownership state of a marker-owned resource adopted from an index whose live object
// does not carry its markers yet: the first `plunge up` writes them in place.
const MarkerAdoptedPending MarkerState = "adopted-markers-pending"

// Refusal codes of an index adoption, beside the checkpoint adoption's (adopt.go). Each names the resource it is
// about, except stock-checkpoint.
const (
	// RefuseEngineType is a selection with no Pulumi type: its inventory type joins to none or to several and the
	// selection names none, or the type it names is not one the inventory type joins to.
	RefuseEngineType AdoptRefusalCode = "engine-type"
	// RefuseAddress is a selection whose `as` is not a resource name or a URN of this stack.
	RefuseAddress AdoptRefusalCode = "address"
	// RefuseNotImportable is a type pkg/identity classes as not importable.
	RefuseNotImportable AdoptRefusalCode = "not-importable"
	// RefuseImportID is a node whose import ID cannot be worked out from what the index holds.
	RefuseImportID AdoptRefusalCode = "import-id"
	// RefuseForeignMarker is a live object carrying another stack's marker, or this stack's marker for another
	// resource.
	RefuseForeignMarker AdoptRefusalCode = "foreign-marker"
	// RefuseLiveReadFailed is a node the stack's provider could not be loaded for, or could not read.
	RefuseLiveReadFailed AdoptRefusalCode = "live-read-failed"
	// RefuseNotFound is a node the live read did not find.
	RefuseNotFound AdoptRefusalCode = "not-found"
	// RefuseStockCheckpoint is a stack whose DIY checkpoint holds resources, which `plunge stack adopt` without
	// --adopt adopts.
	RefuseStockCheckpoint AdoptRefusalCode = "stock-checkpoint"
)

// IndexProviders hands an index adoption the stack's configured default provider for a package, and the provider
// state its record holds.
type IndexProviders interface {
	Provider(ctx context.Context, pkg tokens.Package) (plugin.Provider, *pkgresource.State, error)
}

// IndexAdoptOptions is an adoption from an adoption file, in AdoptOptions.Index.
type IndexAdoptOptions struct {
	// Adoption is the adoption file resolved against its index version (adoption.Open).
	Adoption *adoption.Adoption
	// File names the adoption file, for the record.
	File string
	// Providers configures the stack's providers.
	Providers IndexProviders
	// SecretsManager encrypts the secret values a Read returns before they reach a record. Nil refuses a Read
	// that returns one.
	SecretsManager secrets.Manager

	// accounts and readModel stand in for STS and Cloud Control in tests; nil calls them on the stack's endpoints.
	accounts  *accountIDs
	readModel func(ctx context.Context, info TypeInfo, inputs resource.PropertyMap, identifier string) (
		map[string]any, error)
}

// adoptionIndex is what an index adoption's record holds about the file it read.
type adoptionIndex struct {
	File     string `json:"file,omitempty"`
	ScopeKey string `json:"scopeKey"`
	Version  string `json:"version"`
	Digest   string `json:"digest"`
}

// MarkerWrite is the one change the first `plunge up` makes to an adopted object: these tags (or labels), set in
// Property, and nothing else.
type MarkerWrite struct {
	Property string            `json:"property"`
	Tags     map[string]string `json:"tags"`
}

// AdoptedIndex is the index version an index adoption read, in AdoptResult.
type AdoptedIndex struct {
	File     string
	ScopeKey string
	Version  string
	Digest   string
}

// adoptionFileDigest is the digest an index adoption's record holds: of the adoption file, in canonical JSON.
func adoptionFileDigest(f *adoption.File) (string, error) {
	raw, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	canon, err := canonicalJSON(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}

// planned is one selection as the live read found it.
type planned struct {
	entry adoptedResource
	state *pkgresource.State
	info  TypeInfo
}

// adoptIndex is Adopt for opts.Index.
func (b *plungeBackend) adoptIndex(ctx context.Context, s backend.Stack, opts AdoptOptions) (*AdoptResult, error) {
	ix := opts.Index
	if ix.Adoption == nil || ix.Adoption.File == nil || ix.Adoption.Index == nil {
		return nil, fmt.Errorf("plunge: an index adoption needs the adoption file resolved against its index version")
	}
	if ix.Providers == nil {
		return nil, fmt.Errorf("plunge: an index adoption needs the stack's providers to read each object live")
	}
	ref := s.Ref()
	keys, err := keysFor(ref)
	if err != nil {
		return nil, err
	}
	project, _ := ref.Project()
	stackName := ref.Name().Q()
	l, err := load(ctx, b.store, keys)
	if err != nil {
		return nil, err
	}

	f := ix.Adoption.File
	digest, err := adoptionFileDigest(f)
	if err != nil {
		return nil, fmt.Errorf("plunge: reading the adoption file %s: %w", ix.File, err)
	}
	index := &AdoptedIndex{File: ix.File, ScopeKey: f.Index.ScopeKey, Version: f.Index.Version, Digest: f.Index.Digest}
	source := fmt.Sprintf("the adoption file %s (index %s/%s)", ix.File, f.Index.ScopeKey, f.Index.Version)

	if l.adoption != nil && l.adoption.CheckpointDigest == digest {
		res := &AdoptResult{AlreadyAdopted: true, Source: l.adoption.Source, Digest: digest, Index: index}
		for _, r := range l.adoption.Resources {
			res.Resources = append(res.Resources, r.result())
		}
		return res, nil
	}
	if l.adoption != nil {
		return nil, newAdoptRefusedError(ref.String(), []AdoptRefusal{{Code: RefuseOtherCheckpoint, Reason: fmt.Sprintf(
			"the stack was already adopted from %s (digest %s, at %s); this adoption file has digest %s",
			l.adoption.Source, l.adoption.CheckpointDigest, l.adoption.AdoptedAt, digest)}},
			&AdoptResult{Source: source, Digest: digest, Index: index})
	}

	r := &indexRead{
		opts:    ix,
		cat:     newCatalog(),
		stack:   stackName,
		project: tokens.PackageName(project),
		provs:   map[tokens.Package]*indexProvider{},
	}
	if r.accounts = ix.accounts; r.accounts == nil {
		r.accounts = &accountIDs{}
	}
	var plans []planned
	var reasons []AdoptRefusal
	for _, e := range ix.Adoption.Entries {
		p, refusal := r.read(ctx, e)
		if refusal != nil {
			reasons = append(reasons, *refusal)
		}
		if p != nil {
			plans = append(plans, *p)
		}
	}
	reasons = append(r.providerRefusals, reasons...)
	sort.Slice(plans, func(i, j int) bool { return plans[i].entry.URN < plans[j].entry.URN })

	rec := &adoptionRecord{Format: adoptionFormat, Source: source, CheckpointDigest: digest, Index: &adoptionIndex{
		File: ix.File, ScopeKey: f.Index.ScopeKey, Version: f.Index.Version, Digest: f.Index.Digest,
	}}
	res := &AdoptResult{Source: source, Digest: digest, Index: index}
	for _, p := range plans {
		rec.Resources = append(rec.Resources, p.entry)
		res.Resources = append(res.Resources, p.entry.result())
	}

	// A stock checkpoint holding resources is the other adoption's to take.
	if n, err := b.stockResources(ctx, s, opts.SecretsProvider); err != nil {
		return nil, err
	} else if n > 0 {
		reasons = append(reasons, AdoptRefusal{Code: RefuseStockCheckpoint, Reason: fmt.Sprintf(
			"the stack's DIY checkpoint holds %d resources; adopt it with `plunge stack adopt` (without --adopt) "+
				"instead, since a stack has one adoption", n)})
	}

	// Records already present: only a resumed adoption of this same file is finished.
	resume := false
	if hasResourceRecords(l) {
		if sameAdopted(l.deployment.Deployment.Resources, plans) {
			resume = true
		} else {
			reasons = append(reasons, AdoptRefusal{Code: RefuseForeignRecords, Reason: "the stack already has plunge " +
				"records that are not this adoption's; adopt from an index only into a stack plunge has never run against"})
		}
	}
	if len(reasons) > 0 {
		return nil, newAdoptRefusedError(ref.String(), reasons, res)
	}
	if !opts.Confirmed {
		return nil, &AdoptUnconfirmedError{Stack: ref.String(), Result: res}
	}

	if !resume {
		d, err := r.deployment(ctx, plans)
		if err != nil {
			return nil, err
		}
		led := newLedger()
		for _, p := range plans {
			p := p
			led.record(p.entry.URN, func(e *ownershipEntry) {
				e.By, e.Family, e.TFType = p.entry.Ownership, p.info.Family, p.info.TFType
				if p.info.Identity != nil {
					e.Identity = p.info.Identity.Claim()
				}
				switch {
				case p.entry.Ownership == OwnershipRecord:
					e.State, e.Reason = MarkerUntaggable, p.entry.Reason
				case p.entry.Marked:
					e.State = MarkerInjected
					e.Reason = "adopted from " + source + "; the live object already carries its markers"
				default:
					e.State = MarkerAdoptedPending
					e.Reason = "adopted from " + source + "; its markers are written at the first `plunge up`"
				}
			})
		}
		p := newRecordPersister(ctx, b.store, l, Generation{}, false, nil)
		p.ownership = led
		if err := p.Save(apitype.TypedDeployment{Deployment: d, Version: apitype.DeploymentSchemaVersionCurrent}); err != nil {
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

// stockResources counts the custom, non-provider resources the stack's DIY checkpoint holds.
func (b *plungeBackend) stockResources(ctx context.Context, s backend.Stack, sp secrets.Provider) (int, error) {
	snap, err := unwrap(s).Snapshot(ctx, sp)
	if err != nil {
		return 0, fmt.Errorf("plunge: reading the DIY checkpoint of %s to check that it holds nothing to adopt: %w",
			s.Ref(), err)
	}
	if snap == nil {
		return 0, nil
	}
	n := 0
	for _, r := range snap.Resources {
		if r.Custom && !sdkproviders.IsProviderType(r.Type) && !r.Delete {
			n++
		}
	}
	return n, nil
}

func hasResourceRecords(l *loaded) bool {
	for _, k := range l.records {
		if k.resource != nil {
			return true
		}
	}
	return false
}

// sameAdopted reports whether the stack's records hold exactly the planned resources, by URN, type and ID.
func sameAdopted(have []apitype.ResourceV3, plans []planned) bool {
	got := map[string]bool{}
	for _, r := range have {
		if !r.Delete && r.Custom && !sdkproviders.IsProviderType(r.Type) {
			got[string(r.URN)+"|"+string(r.Type)+"|"+string(r.ID)] = true
		}
	}
	if len(got) != len(plans) {
		return false
	}
	for _, p := range plans {
		if !got[string(p.entry.URN)+"|"+string(p.entry.Type)+"|"+string(p.entry.ID)] {
			return false
		}
	}
	return true
}

// indexProvider is one package's provider, loaded once per adoption.
type indexProvider struct {
	prov  plugin.Provider
	state *pkgresource.State
	err   error
}

// indexRead reads one adoption's selections live.
type indexRead struct {
	opts     *IndexAdoptOptions
	cat      *catalog
	stack    tokens.QName
	project  tokens.PackageName
	accounts *accountIDs

	provs map[tokens.Package]*indexProvider
	// providerRefusals are the provider configurations refused, once each.
	providerRefusals []AdoptRefusal
}

// provider is pkg's provider, loaded and vetted once.
func (r *indexRead) provider(ctx context.Context, pkg tokens.Package) *indexProvider {
	if p, ok := r.provs[pkg]; ok {
		return p
	}
	p := &indexProvider{}
	p.prov, p.state, p.err = r.opts.Providers.Provider(ctx, pkg)
	if p.err == nil && p.state != nil {
		if err := checkProviderInputs(p.state.URN, p.state.Inputs); err != nil {
			r.providerRefusals = append(r.providerRefusals,
				AdoptRefusal{Code: RefuseHiddenMarkers, URN: p.state.URN, Reason: err.Error()})
		}
	}
	r.provs[pkg] = p
	return p
}

// read decides one selection: its plan, or why it is refused.
func (r *indexRead) read(ctx context.Context, e adoption.Entry) (*planned, *AdoptRefusal) {
	n := e.Node
	substrate, _, _ := strings.Cut(n.ID, "/")

	cands := typeCandidates(substrate, n.ListedType)
	if e.Type != "" && substrate == "aws" {
		// A named aws type may be pulumi-aws's as well as aws-native's: it is checked against the registry's join
		// of the node's CloudFormation type once the provider's schema is read, below.
		cands = nil
	}
	typ, err := e.EngineType(cands)
	if err != nil {
		return nil, &AdoptRefusal{Code: RefuseEngineType, Reason: err.Error()}
	}
	t := tokens.Type(typ)
	if !tokens.IsQName(string(t.Name())) || t.Package() == "" {
		return nil, &AdoptRefusal{Code: RefuseEngineType, Reason: fmt.Sprintf("%s: %q is not a Pulumi type token", n.ID, typ)}
	}
	urn, err := r.address(e.As, t)
	if err != nil {
		return nil, &AdoptRefusal{Code: RefuseAddress, Reason: fmt.Sprintf("%s: %v", n.ID, err)}
	}
	refuse := func(code AdoptRefusalCode, format string, args ...any) (*planned, *AdoptRefusal) {
		return nil, &AdoptRefusal{Code: code, URN: urn, Reason: fmt.Sprintf("%s (node %s): ", urn, n.ID) +
			fmt.Sprintf(format, args...)}
	}

	ip := r.provider(ctx, t.Package())
	if ip.err != nil {
		return refuse(RefuseLiveReadFailed, "the stack's %s provider could not be loaded and configured: %v",
			t.Package(), ip.err)
	}
	info := r.cat.lookup(ctx, ip.prov, t)
	if info.Family.bridged() && info.TFType == "" && info.Shape == ShapeUnknown {
		return refuse(RefuseEngineType, "the %s provider's schema has no %s", t.Package(), t)
	}
	if substrate == "aws" && info.CFNType != "" && n.ListedType != "" && info.CFNType != n.ListedType {
		return refuse(RefuseEngineType, "%s is listed through %s, not the node's %s", t, info.CFNType, n.ListedType)
	}
	if info.TFType != "" && identity.NotImportable(info.TFType) {
		return refuse(RefuseNotImportable, "choudoufu's pkg/identity classes %s as not importable", info.TFType)
	}

	entry := adoptedResource{URN: urn, Type: t, Node: n.ID, Ownership: OwnershipMarker}
	owned, why := recordOwned(info)
	var want map[string]string
	if owned {
		entry.Ownership, entry.Reason = OwnershipRecord, why
	} else {
		// The type table decides first, before any provider call.
		if info.CreateOnly {
			return refuse(RefuseCreateOnlyTags, "%s", (&CreateOnlyTagsError{URN: urn, Type: t, Property: info.Property}).Error())
		}
		if want, err = markers.For(urn, info.Encoding()); err != nil {
			return refuse(RefuseUnrepresentableMarkers, "%v", err)
		}
	}

	id, err := r.importID(ctx, info, ip.state, substrate, n)
	if err != nil {
		code := RefuseImportID
		var skip *SkippedListing
		if errors.As(err, &skip) {
			code = RefuseLiveReadFailed
		}
		return refuse(code, "%v", err)
	}
	entry.ID, entry.ImportID = id, id

	resp, err := ip.prov.Read(ctx, plugin.ReadRequest{URN: urn, Name: urn.Name(), Type: t, ID: id})
	switch {
	case err != nil && resp.Status != resource.StatusPartialFailure:
		return refuse(RefuseLiveReadFailed, "the stack's %s provider could not read %s: %v", t.Package(), id, err)
	case resp.Outputs == nil || resp.ID == "":
		return refuse(RefuseNotFound, "the stack's %s provider found no %s %s; the index version is older than the "+
			"object's deletion, or the stack's provider looks somewhere else", t.Package(), t, id)
	}
	outputs := resource.ToResourcePropertyMap(*resp.Outputs)
	inputs := outputs.Copy()
	if resp.Inputs != nil {
		inputs = resource.ToResourcePropertyMap(*resp.Inputs)
	}
	entry.ID = resp.ID

	if !owned {
		tags := tagsOf(outputs, info)
		_, detail, carriesOurs := liveOwnerRefusal(tags, want)
		switch {
		case detail != "":
			return refuse(RefuseForeignMarker, "%s; leave it out of the adoption file, or move it out of that "+
				"stack first", detail)
		case carriesOurs:
			entry.Marked = true
		default:
			entry.MarkerWrite = &MarkerWrite{Property: info.Property, Tags: want}
		}
	}

	st := pkgresource.NewState{
		Type:     t,
		URN:      urn,
		Custom:   true,
		ID:       entry.ID,
		Inputs:   inputs,
		Outputs:  outputs,
		Parent:   r.rootURN(),
		Provider: ip.ref(),
		ImportID: id,
	}.Make()
	return &planned{entry: entry, state: st, info: info}, nil
}

// ref is the provider reference a resource's state holds.
func (p *indexProvider) ref() string {
	if p.state == nil {
		return ""
	}
	return string(p.state.URN) + resource.URNNameDelimiter + string(p.state.ID)
}

func (r *indexRead) rootURN() resource.URN {
	return resource.NewURN(r.stack, r.project, "", resource.RootStackType,
		string(r.project)+"-"+string(r.stack))
}

// address is the URN a selection's `as` names: a resource name, top-level in the stack, or the URN that name has.
func (r *indexRead) address(as string, t tokens.Type) (resource.URN, error) {
	if strings.HasPrefix(as, "urn:") {
		urn, err := resource.ParseURN(as)
		if err != nil {
			return "", fmt.Errorf("as %q is not a URN: %w", as, err)
		}
		want := resource.NewURN(r.stack, r.project, "", t, urn.Name())
		if urn != want {
			return "", fmt.Errorf("as %q is not a top-level %s of this stack (%s); an index adoption adopts each "+
				"object under the root stack, so name it by its resource name", as, t, want)
		}
		return urn, nil
	}
	if as == "" || strings.Contains(as, resource.URNNameDelimiter) {
		return "", fmt.Errorf("as %q is not a resource name", as)
	}
	return resource.NewURN(r.stack, r.project, "", t, as), nil
}

// typeCandidates are the Pulumi types an inventory type joins to without the provider's schema: aws-native's for
// a CloudFormation type, kubernetes's for a group/version/kind. Every other family names its type in the selection.
func typeCandidates(substrate, listed string) []string {
	switch substrate {
	case "aws":
		parts := strings.Split(listed, "::")
		if len(parts) == 3 && parts[0] == "AWS" && parts[1] != "" && parts[2] != "" {
			return []string{"aws-native:" + strings.ToLower(parts[1]) + ":" + parts[2]}
		}
	case "kubernetes", "k8s":
		parts := strings.Split(listed, "/")
		switch len(parts) {
		case 2:
			return []string{"kubernetes:core/" + parts[0] + ":" + parts[1]}
		case 3:
			group := parts[0]
			if group == "" {
				group = "core"
			}
			return []string{"kubernetes:" + group + "/" + parts[1] + ":" + parts[2]}
		}
	}
	return nil
}

// importID is the ID the stack's provider imports node by.
func (r *indexRead) importID(
	ctx context.Context, info TypeInfo, prov *pkgresource.State, substrate string, n *adoption.Node,
) (resource.ID, error) {
	native := n.NativeID
	if native == "" {
		return "", fmt.Errorf("the index node carries no native ID")
	}
	switch info.Family {
	case FamilyAWS:
		if info.CFNType == "" {
			return "", fmt.Errorf("%s has no CloudFormation counterpart, so its Cloud Control identifier cannot be "+
				"translated to its Terraform id", info.Type)
		}
		translate, reason := awsIDTranslation(info)
		if reason != "" {
			return "", fmt.Errorf("%s", reason)
		}
		var inputs resource.PropertyMap
		var region string
		if prov != nil {
			inputs = prov.Inputs
			region = providerString(inputs, "region")
		}
		if region == "" {
			region = firstEnv("AWS_REGION", "AWS_DEFAULT_REGION")
		}
		translate, err := translate.ResolveAccount(ctx, r.accounts.source(r.rootURN(), inputs, region))
		if err != nil {
			return "", err
		}
		if translate == nil {
			return resource.ID(native), nil
		}
		props := n.Attrs
		if translate.WantsModel(native, props) {
			if props, err = r.model(ctx, info, inputs, region, native); err != nil {
				return "", err
			}
		}
		id, ok := translate.Translate(native, props)
		if !ok {
			return "", fmt.Errorf("the Cloud Control identifier %q of %s cannot be translated to %s", native,
				info.CFNType, translate.Describe())
		}
		return resource.ID(id), nil
	case FamilyGoogle:
		if info.TFType == "" {
			return "", fmt.Errorf("%s has no Terraform type to read its import id grammar for", info.Type)
		}
		id, ok := identity.GoogleAssetImportID(info.TFType, native)
		if !ok {
			return "", fmt.Errorf("choudoufu's pkg/identity has no import id for %s from the asset name %q",
				info.TFType, native)
		}
		return resource.ID(id), nil
	}
	// aws-native's Cloud Control identifier, the ARM id as returned, namespace/name.
	return resource.ID(native), nil
}

// model is the object's Cloud Control model, for a translation that reads a property from it: read on the stack's
// own Cloud Control endpoint, and skipped (refused) when the stack names custom endpoints and none for it.
func (r *indexRead) model(
	ctx context.Context, info TypeInfo, inputs resource.PropertyMap, region, identifier string,
) (map[string]any, error) {
	if r.opts.readModel != nil {
		return r.opts.readModel(ctx, info, inputs, identifier)
	}
	endpoint, skip := cloudControlEndpoint(inputs)
	if skip != "" {
		return nil, skipped(r.rootURN(), "AWS Cloud Control", "%s", skip)
	}
	c := cloudcontrol.New(cloudcontrol.Config{Region: region, Endpoint: endpoint, RoundTripper: listerTransport})
	desc, err := cloudcontrol.GetResourceByIdentity(ctx, c, info.CFNType, identifier)
	if err != nil {
		return nil, fmt.Errorf("reading %s %s through Cloud Control for its Terraform id: %w", info.CFNType, identifier, err)
	}
	if desc == nil {
		return nil, fmt.Errorf("Cloud Control found no %s %s", info.CFNType, identifier)
	}
	return desc.Properties, nil
}

// deployment is what the adoption writes: the root stack, each provider used, and each adopted resource.
func (r *indexRead) deployment(ctx context.Context, plans []planned) (*apitype.DeploymentV3, error) {
	sm := r.opts.SecretsManager
	var enc config.Encrypter = config.NewErrorCrypter("plunge: the stack's provider returned a secret value, and " +
		"this adoption has no secrets manager to encrypt it with")
	if sm != nil {
		enc = sm.Encrypter()
	}
	states := []*pkgresource.State{pkgresource.NewState{
		Type: resource.RootStackType, URN: r.rootURN(), Inputs: resource.PropertyMap{}, Outputs: resource.PropertyMap{},
	}.Make()}
	pkgs := make([]string, 0, len(r.provs))
	for pkg := range r.provs {
		pkgs = append(pkgs, string(pkg))
	}
	sort.Strings(pkgs)
	used := map[string]bool{}
	for _, p := range plans {
		used[p.state.Provider] = true
	}
	for _, pkg := range pkgs {
		if p := r.provs[tokens.Package(pkg)]; p.err == nil && p.state != nil && used[p.ref()] {
			states = append(states, p.state)
		}
	}
	for _, p := range plans {
		states = append(states, p.state)
	}

	m := deploy.Manifest{Time: time.Now()}
	m.Magic = m.NewMagic()
	d := &apitype.DeploymentV3{Manifest: m.Serialize()}
	if sm != nil {
		d.SecretsProviders = &apitype.SecretsProvidersV1{Type: sm.Type(), State: sm.State()}
	}
	for _, st := range states {
		sres, _, err := stack.SerializeResource(ctx, st, enc, false)
		if err != nil {
			return nil, fmt.Errorf("plunge: recording %s: %w", st.URN, err)
		}
		d.Resources = append(d.Resources, sres)
	}
	return d, nil
}

// StackProviders loads the stack's default providers for an index adoption, one per package, configured from the
// stack's configuration the way the engine configures a default provider (Target.GetPackageConfig): the package's
// config keys, with the plugin's version. The plugin is the newest one installed; `plunge up` later diffs the
// program's default provider against this one's configuration (DiffConfig), so a different version replaces
// nothing on its own.
type StackProviders struct {
	pctx    *plugin.Context
	stack   tokens.QName
	project tokens.PackageName
	target  *deploy.Target

	mu     sync.Mutex
	loaded []plugin.Provider
}

var _ IndexProviders = (*StackProviders)(nil)

// NewStackProviders is the providers for stack s of project, configured from cfg, decrypted with dec.
func NewStackProviders(
	pctx *plugin.Context, s backend.StackReference, cfg config.Map, dec config.Decrypter,
) (*StackProviders, error) {
	project, ok := s.Project()
	if !ok || project == "" {
		return nil, fmt.Errorf("plunge: the stack %s names no project", s)
	}
	return &StackProviders{
		pctx: pctx, stack: s.Name().Q(), project: tokens.PackageName(project),
		target: &deploy.Target{Config: cfg, Decrypter: dec},
	}, nil
}

// Provider implements IndexProviders.
func (sp *StackProviders) Provider(ctx context.Context, pkg tokens.Package) (plugin.Provider, *pkgresource.State, error) {
	cfg, err := sp.target.GetPackageConfig(pkg)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the stack's %s configuration: %w", pkg, err)
	}
	inputs := resource.ToResourcePropertyMap(cfg)

	prov, err := sp.pctx.Host.Provider(sp.pctx, workspace.PluginDescriptor{
		Kind: apitype.ResourcePlugin, Name: string(pkg),
	}, env.Global())
	if err != nil {
		return nil, nil, fmt.Errorf("loading the %s plugin: %w", pkg, err)
	}
	if prov == nil {
		return nil, nil, fmt.Errorf("no %s plugin is installed (`plunge plugin install resource %s`)", pkg, pkg)
	}
	sp.mu.Lock()
	sp.loaded = append(sp.loaded, prov)
	sp.mu.Unlock()

	var version *semver.Version
	if info, err := prov.GetPluginInfo(ctx); err == nil && info.Version != nil {
		version = info.Version
		deployproviders.SetProviderVersion(inputs, version)
	}
	name := deployproviders.NewProviderRequest(pkg, version, "", nil, nil).DefaultName()
	urn := resource.NewURN(sp.stack, sp.project, "", sdkproviders.MakeProviderType(pkg), name)
	id := resource.ID(uuid.NewString())
	typ := urn.Type()
	qname := urn.Name()
	if _, err := prov.Configure(ctx, plugin.ConfigureRequest{
		URN: &urn, Name: &qname, Type: &typ, ID: &id,
		Inputs: resource.FromResourcePropertyMap(deployproviders.FilterProviderConfig(inputs)),
	}); err != nil {
		return nil, nil, fmt.Errorf("configuring the %s provider from the stack's configuration: %w", pkg, err)
	}
	st := pkgresource.NewState{
		Type: typ, URN: urn, Custom: true, ID: id, Inputs: inputs, Outputs: inputs.Copy(),
	}.Make()
	return prov, st, nil
}

// Close closes every provider loaded.
func (sp *StackProviders) Close() error {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	for _, p := range sp.loaded {
		_ = p.Close()
	}
	sp.loaded = nil
	return nil
}
