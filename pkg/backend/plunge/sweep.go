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

// The unrecorded-object sweep (INTENTIUS/plunge#18, ruled 2026-10-26). When two runs race to create one resource
// and both creates land, the second record write is refused (RecordConflictError) and the next run plans from the
// winner's record. Bind runs only for creates, so nothing would ever look at the loser's object again: it carries
// the stack's markers and no record names it. choudoufu's orphan leg reports the same case for its own estates.
//
// After the bind phase, once the run's records are written, the sweep lists each covered type the run marked with
// the stack's marker alone (plunge-stack) and reports, by type and ID, every live object that carries it and that no
// record of the stack names. It warns and never deletes: which of the two objects to keep is the operator's call.
//
// Every listing that reads the service's own store is swept: Cloud Control, ARM, Kubernetes and gcplist's service
// lists, whether or not their consistency is claimed for C2 (INTENTIUS/plunge#15). A lagging index is not: Cloud
// Asset Inventory can still list an object a record no longer names because it was deleted a moment ago, and a
// report from it would be a false one.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// sweepTarget is what the sweep needs to list one marked resource's type: what the catalog said about the type, and,
// for a create, the request the binder listed it with.
type sweepTarget struct {
	info TypeInfo
	req  *deploy.BindRequest
}

// UnrecordedObject is a live object carrying a stack's ownership marker that no record of the stack names.
type UnrecordedObject struct {
	Type tokens.Type
	ID   resource.ID
	// Claims is the URN the object's markers name, when it can be read back (the tag families carry the URN, the
	// label families a digest matched against the URNs the stack knows). Empty when it cannot.
	Claims resource.URN
	// Listing is what the sweep listed the type through.
	Listing string
}

func (o UnrecordedObject) String() string {
	claims := ""
	if o.Claims != "" {
		claims = fmt.Sprintf(", and its markers name %s", o.Claims)
	}
	return fmt.Sprintf("plunge: unrecorded marked object: the %s %s carries this stack's ownership marker%s, but "+
		"no record of the stack names it (listed through %s). A racing create whose record write lost leaves one "+
		"behind, and so does a run still in flight that has not recorded its create yet. plunge does not delete "+
		"it: if a second run reports it too, delete it if it is a duplicate, or import it into the stack",
		o.Type, o.ID, claims, o.Listing)
}

// sweepNote is one type the sweep could not list, with the reason. warn is set for a listing skipped because the
// stack points its provider at custom endpoints and names none for the service (INTENTIUS/plunge#27).
type sweepNote struct {
	text string
	warn bool
}

// sweepUnrecorded lists every covered type marks says the run marked, with the stack's marker, and returns the live
// objects no record under keys names. notes are the types it could not list, each with the reason.
func sweepUnrecorded(
	ctx context.Context, store staterecord.Store, keys stackKeys, marks *ledger, listers map[Family]Lister,
) (found []UnrecordedObject, notes []sweepNote, err error) {
	targets := marks.sweepTargets()
	if len(targets) == 0 {
		return nil, nil, nil
	}
	l, err := load(ctx, store, keys)
	if err != nil {
		return nil, nil, err
	}
	var states []apitype.ResourceV3
	if d := l.deployment.Deployment; d != nil {
		states = d.Resources
		for _, op := range d.PendingOperations {
			states = append(states, op.Resource)
		}
	}
	recorded := map[resource.ID]bool{}
	byURN := map[resource.URN]apitype.ResourceV3{}
	known := map[resource.URN]bool{}
	for _, s := range states {
		if s.ID != "" {
			recorded[s.ID] = true
		}
		if s.ImportID != "" {
			recorded[s.ImportID] = true
		}
		if !s.Delete {
			byURN[s.URN] = s
		}
		known[s.URN] = true
	}
	for urn := range targets {
		known[urn] = true
	}
	aliases := recordAliases(states, targets)

	urns := make([]resource.URN, 0, len(targets))
	for urn := range targets {
		urns = append(urns, urn)
	}
	sort.Slice(urns, func(i, j int) bool { return urns[i] < urns[j] })

	listed := map[string]bool{}
	reported := map[resource.ID]bool{}
	for _, urn := range urns {
		t := targets[urn]
		req, ok := t.req, t.req != nil
		if !ok {
			r, ok2 := requestFromRecords(urn, t.info, byURN)
			if !ok2 {
				// The resource has no record (its first write failed for another reason): nothing to list with.
				continue
			}
			req = &r
		}
		lister, ok := listers[t.info.Family]
		if !ok {
			continue
		}
		if covered, _ := lister.Covers(t.info); !covered {
			continue
		}
		if listsAnIndex(lister, t.info, *req) {
			continue
		}
		listing := fmt.Sprintf("%s family lister", t.info.Family)
		if d, ok := lister.(listingDescriber); ok {
			listing = d.Listing(t.info, *req)
		}
		key := string(t.info.Type) + "\x00" + string(req.ProviderURN) + "\x00" + listing
		if listed[key] {
			continue
		}
		listed[key] = true

		want := map[string]string{markers.TagStack: mustMarkers(urn, t.info)[markers.TagStack]}
		objs, err := lister.ListMarked(ctx, t.info, *req, want)
		var skip *SkippedListing
		if errors.As(err, &skip) {
			notes = append(notes, sweepNote{text: fmt.Sprintf("the sweep for unrecorded %s objects did not run: %v",
				t.info.Type, skip), warn: true})
			continue
		}
		if err != nil {
			notes = append(notes, sweepNote{text: fmt.Sprintf("the sweep for unrecorded %s objects could not list "+
				"them: %v", t.info.Type, err)})
			continue
		}
		for _, o := range objs {
			if recorded[o.ID] || aliases[t.info.Type][string(o.ID)] || reported[o.ID] {
				continue
			}
			reported[o.ID] = true
			found = append(found, UnrecordedObject{
				Type: t.info.Type, ID: o.ID, Claims: claimedURN(o.Tags, t.info, known), Listing: listing,
			})
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Type != found[j].Type {
			return found[i].Type < found[j].Type
		}
		return found[i].ID < found[j].ID
	})
	return found, notes, nil
}

// recordAliases are, by type, the other names the records give each recorded object of a swept type: the string
// values of its ARN, URL and name outputs, and of the outputs its import id row in pkg/identity names. A listing
// translates each Cloud Control identifier to pulumi-aws's id before the lookup (ccid_single.go), and this is the
// second chance for an identifier the listing could not translate: an emulator that lists a queue by its name, or a
// function by its ARN, still names an object a record holds, and that object is not reported (INTENTIUS/plunge#28).
// Each name is unique among a type's objects, so a match is the recorded object itself.
func recordAliases(
	states []apitype.ResourceV3, targets map[resource.URN]sweepTarget,
) map[tokens.Type]map[string]bool {
	keys := map[tokens.Type][]string{}
	for _, t := range targets {
		if _, ok := keys[t.info.Type]; ok {
			continue
		}
		keys[t.info.Type] = aliasOutputs(t.info)
	}
	out := map[tokens.Type]map[string]bool{}
	for _, s := range states {
		ks, ok := keys[s.Type]
		if !ok || s.ID == "" {
			continue
		}
		for _, k := range ks {
			v, _ := s.Outputs[k].(string)
			if v == "" || resource.ID(v) == s.ID {
				continue
			}
			if out[s.Type] == nil {
				out[s.Type] = map[string]bool{}
			}
			out[s.Type][v] = true
		}
	}
	return out
}

// aliasOutputs are the Pulumi output names recordAliases reads for info's type.
func aliasOutputs(info TypeInfo) []string {
	seen := map[string]bool{}
	var out []string
	add := func(tf string) {
		k := lowerCamel(tf)
		if k == "" || k == "id" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, k)
	}
	for _, k := range []string{"arn", "url", "name"} {
		add(k)
	}
	if info.Identity != nil {
		for _, a := range info.Identity.IdentityAttrs {
			add(a)
		}
		for _, c := range info.Identity.Components {
			if c.Cloud == "" && len(c.Attrs) == 1 {
				add(c.Attrs[0])
			}
		}
	}
	return out
}

// lowerCamel is a Terraform attribute name in the bridge's Pulumi spelling: "function_name" is "functionName".
func lowerCamel(tf string) string {
	parts := strings.Split(tf, "_")
	var b strings.Builder
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i == 0 {
			b.WriteString(p)
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

// listsAnIndex reports whether lister would list info's type through a lagging index rather than the service's own
// store: Cloud Asset Inventory, the fallback for a Google Cloud type gcplist does not cover.
func listsAnIndex(lister Lister, info TypeInfo, req deploy.BindRequest) bool {
	switch l := lister.(type) {
	case *assetLister:
		return true
	case *gcpLister:
		return l.plan(info, req).fallback != ""
	}
	return false
}

// requestFromRecords builds the listing request for urn from its recorded state and its provider's: the inputs the
// type's list path is filled from, and the provider's region, project and credentials.
func requestFromRecords(
	urn resource.URN, info TypeInfo, byURN map[resource.URN]apitype.ResourceV3,
) (deploy.BindRequest, bool) {
	s, ok := byURN[urn]
	if !ok {
		return deploy.BindRequest{}, false
	}
	req := deploy.BindRequest{URN: urn, Type: info.Type, Inputs: resource.NewPropertyMapFromMap(s.Inputs)}
	if s.Provider != "" {
		if ref, err := providers.ParseReference(s.Provider); err == nil {
			req.ProviderURN = ref.URN()
			if p, ok := byURN[ref.URN()]; ok {
				req.ProviderInputs = resource.NewPropertyMapFromMap(p.Inputs)
			}
		}
	}
	return req, true
}

// claimedURN is the URN an object's markers name, among the URNs the stack knows when the markers carry a digest.
func claimedURN(tags map[string]string, info TypeInfo, known map[resource.URN]bool) resource.URN {
	_, short, ok := markers.Owner(tags)
	if !ok || short == "" {
		return ""
	}
	for urn := range known {
		m, err := markers.For(urn, info.Encoding())
		if err != nil {
			continue
		}
		if info.Encoding() == markers.EncodingLabels {
			if m[markers.TagURN] == short {
				return urn
			}
			continue
		}
		var b strings.Builder
		for i := 0; i < markers.MaxContinuations; i++ {
			chunk, has := m[markers.ContinuationKey(i)]
			if !has {
				break
			}
			b.WriteString(chunk)
		}
		if b.String() == short {
			return urn
		}
	}
	return ""
}

// reportUnrecorded runs the sweep after a run and warns once per unrecorded object, on the URN its markers name when
// that is known. A sweep that cannot run is a note, never a failure of the run.
func reportUnrecorded(
	ctx context.Context, d diag.Sink, store staterecord.Store, keys stackKeys, marks *ledger,
	listers map[Family]Lister,
) []UnrecordedObject {
	if d == nil || marks == nil {
		return nil
	}
	found, notes, err := sweepUnrecorded(ctx, store, keys, marks, listers)
	if err != nil {
		d.Infof(diag.RawMessage("", fmt.Sprintf("plunge: the sweep for unrecorded marked objects did not run: %v\n",
			err)))
		return nil
	}
	for _, n := range notes {
		if n.warn {
			d.Warningf(diag.RawMessage("", "plunge: "+n.text+"\n"))
			continue
		}
		d.Infof(diag.RawMessage("", "plunge: "+n.text+"\n"))
	}
	for _, o := range found {
		d.Warningf(diag.Message(o.Claims, "%s"), o.String())
	}
	return found
}
