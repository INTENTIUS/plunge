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

// The marker-bind and collision phase (INTENTIUS/plunge#7, decision 2 of the parity comment on #2). Before the step
// for a create is generated, the binder lists the type's live objects carrying the resource's markers:
//
//   - none: the create goes ahead.
//   - one: it is bound. The create becomes an import of that object, which is how a run that crashed after the
//     create, or a second run racing the first, converges on one object (claims C1 and C6).
//   - two or more: a collision. The run is refused naming every ID (claim C2), as choudoufu's collisionProblem
//     does. Nothing is created or deleted.
//
// Binding needs a listing that is consistent per type, so each family has one lister (listers.go):
//
//   - AWS and aws-native: Cloud Control ListResources on the CloudFormation type, filtered by tag client-side.
//     aws-native names its CloudFormation type; a pulumi-aws type is joined to one through choudoufu's registry.
//   - Azure and azure-native: ARM's own resource list filtered by the URN tag (not Resource Graph, whose index lags).
//   - Kubernetes: a LIST of the kind by label selector, which the API server answers from a quorum read.
//   - Google Cloud: each covered type's own service list (package gcplist, INTENTIUS/plunge#15, ruled 2026-10-25),
//     filtered by label client-side. A type gcplist does not cover is searched through Cloud Asset Inventory, which
//     is eventually consistent: it binds, but it cannot carry the collision claim, and the ownership entry says so.
//
// SDKv2-bridged types have no provider List (pulumi-terraform-bridge pkg/tfbridge/provider.go:2065-2072), so the
// provider is never asked. Decision taken: an SDKv2 type is covered exactly when its family lister covers its
// cloud type (for pulumi-aws, when the registry joins it to a CloudFormation type Cloud Control lists without a
// parent's input). A type with no lister is "unlisted": no bind or collision check runs, its record says why, and
// C1, C2 and C6 do not cover it, the way choudoufu's readiness table carves out types it cannot list.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// LiveObject is one live object a lister found carrying the markers asked for.
type LiveObject struct {
	ID   resource.ID
	Tags map[string]string
}

// Lister lists one family's live objects of a type that carry a set of markers.
type Lister interface {
	// Covers reports whether the lister can list info's type. A false answer says why.
	Covers(info TypeInfo) (bool, string)
	// Consistent reports whether a listing is strongly consistent, which is what the collision claim needs.
	Consistent() bool
	// ListMarked returns every live object of info's type carrying want.
	ListMarked(ctx context.Context, info TypeInfo, req deploy.BindRequest, want map[string]string) ([]LiveObject, error)
}

// requestConsistency is a Lister whose consistency depends on the type and the request (Google Cloud: a covered
// type whose list is addressable is consistent, anything else falls back to Asset Inventory). The reason says why a
// listing is not consistent.
type requestConsistency interface {
	ConsistentFor(info TypeInfo, req deploy.BindRequest) (bool, string)
}

// listingDescriber is a Lister that can say, for the ownership entry, what it listed through.
type listingDescriber interface {
	Listing(info TypeInfo, req deploy.BindRequest) string
}

// CollisionError refuses a create whose markers are carried by more than one live object. It names every ID, so the
// operator can see which objects claim to be the same resource.
type CollisionError struct {
	URN resource.URN
	IDs []resource.ID
}

func (e *CollisionError) Error() string {
	ids := make([]string, len(e.IDs))
	for i, id := range e.IDs {
		ids[i] = string(id)
	}
	return fmt.Sprintf("plunge: refusing to create %s: %d live objects already carry its ownership markers (%s), so "+
		"there is no one object to bind it to. Delete the duplicates you do not want, or remove their markers, "+
		"and re-run", e.URN, len(e.IDs), strings.Join(ids, " and "))
}

// binder is the plunge backend's deploy.MarkerBinder.
type binder struct {
	catalog *catalog
	ledger  *ledger
	listers map[Family]Lister
}

var _ deploy.MarkerBinder = (*binder)(nil)

func newBinder(c *catalog, l *ledger, listers map[Family]Lister) *binder {
	return &binder{catalog: c, ledger: l, listers: listers}
}

// defaultListers are the family listers, built lazily per provider configuration.
func defaultListers() map[Family]Lister {
	aws := &cloudControlLister{}
	az := &armLister{}
	return map[Family]Lister{
		FamilyAWS:         aws,
		FamilyAWSNative:   aws,
		FamilyAzure:       az,
		FamilyAzureNative: az,
		FamilyKubernetes:  &kubeLister{},
		FamilyGoogle:      newGCPLister(),
	}
}

// Bind implements deploy.MarkerBinder.
func (b *binder) Bind(ctx context.Context, req deploy.BindRequest) (resource.ID, error) {
	info := b.catalog.lookup(ctx, req.Provider, req.Type)
	if info.Shape != ShapeMap && info.Shape != ShapeKeyValueArray {
		// No marker is written for the type, so there is nothing to bind by. Its record carries ownership.
		return "", nil
	}
	if !carries(req.Inputs, info, mustMarkers(req.URN, info)) {
		// The injector could not merge the markers (unknown tags at preview): nothing to look for yet.
		return "", nil
	}
	want := mustMarkers(req.URN, info)
	lister, ok := b.listers[info.Family]
	reason := ""
	if ok {
		ok, reason = lister.Covers(info)
	} else {
		reason = fmt.Sprintf("plunge has no lister for the %q family", info.Family)
	}
	if !ok {
		b.ledger.record(req.URN, func(e *ownershipEntry) { e.Unlisted = reason })
		info0(req, "plunge: no marker bind or collision check for this create: %s", reason)
		return b.readByName(ctx, info, req, want), nil
	}
	consistent, why := lister.Consistent(), ""
	if rc, ok := lister.(requestConsistency); ok {
		consistent, why = rc.ConsistentFor(info, req)
	}
	listing := fmt.Sprintf("%s family lister", info.Family)
	if d, ok := lister.(listingDescriber); ok {
		listing = d.Listing(info, req)
	}
	b.ledger.record(req.URN, func(e *ownershipEntry) { e.Listing = listing })
	sweepReq := req
	sweepReq.Diag = nil
	b.ledger.noteMarked(req.URN, info, &sweepReq)
	found, err := lister.ListMarked(ctx, info, req, want)
	if err != nil {
		// A listing that failed is a coverage gap for this run, not grounds to refuse it or to bind.
		b.ledger.record(req.URN, func(e *ownershipEntry) { e.Unlisted = "the listing failed: " + err.Error() })
		info0(req, "plunge: the marker listing for this create failed, so no bind or collision check ran: %v", err)
		return b.readByName(ctx, info, req, want), nil
	}
	if !consistent {
		if why == "" {
			why = fmt.Sprintf("the %s listing is eventually consistent", info.Family)
		}
		unlisted := why + ", so it binds but does not prove the absence of a duplicate"
		b.ledger.record(req.URN, func(e *ownershipEntry) { e.Unlisted = unlisted })
		info0(req, "plunge: the collision check does not cover this create: %s", unlisted)
	}
	switch len(found) {
	case 0:
		if !consistent {
			// The index may not hold the winner yet (Cloud Asset Inventory lags a create).
			return b.readByName(ctx, info, req, want), nil
		}
		return "", nil
	case 1:
		b.ledger.record(req.URN, func(e *ownershipEntry) { e.Bound = found[0].ID })
		return found[0].ID, nil
	}
	ids := make([]resource.ID, len(found))
	for i, o := range found {
		ids[i] = o.ID
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return "", &CollisionError{URN: req.URN, IDs: ids}
}

// readByName is the bind for a C1-covered type no lister covers, whose listing failed, or whose eventually
// consistent listing found nothing (INTENTIUS/plunge#10). When the winner of a racing create stopped after its
// create and before its record write, no listing finds it, and the loser's create is refused by the cloud on
// every re-run. For a type whose import ID is its name, the one name a create would ask
// for is known before Check: the program's own, else plunge's deterministic name. A provider Read of that name
// finds the winner; it is bound when it carries this resource's markers. An object with the name but without the
// markers is left alone, and the create then fails at the cloud naming it, as before.
//
// A user `pulumi:autonaming` pattern without a random part also gives a deterministic name, but the binder does
// not evaluate patterns, so for those the read finds nothing and the create goes ahead as it did before.
func (b *binder) readByName(ctx context.Context, info TypeInfo, req deploy.BindRequest, want map[string]string) resource.ID {
	if !info.Autoname.ImportIDIsName || req.Provider == nil {
		return ""
	}
	if covered, _ := c1Covers(info); !covered {
		return ""
	}
	name := DeterministicName(req.URN, info.Autoname.MaxLength, info.Autoname.Suffix)
	if v, ok := req.Inputs[resource.PropertyKey(info.Autoname.Property)]; ok {
		v = unwrapValue(v)
		if !v.IsString() || v.StringValue() == "" {
			// An unknown or non-string name: nothing to read by yet.
			return ""
		}
		name = v.StringValue()
	}
	resp, err := req.Provider.Read(ctx, plugin.ReadRequest{
		URN:  req.URN,
		Name: req.URN.Name(),
		Type: req.Type,
		ID:   resource.ID(name),
	})
	note := func(s string) { b.ledger.record(req.URN, func(e *ownershipEntry) { e.ByName = s }) }
	switch {
	case err != nil:
		note(fmt.Sprintf("the read of %q failed: %v", name, err))
		return ""
	case resp.ID == "" || resp.Outputs == nil:
		note(fmt.Sprintf("no object is named %q", name))
		return ""
	}
	if !markers.Carries(tagsOf(resource.ToResourcePropertyMap(*resp.Outputs), info), want) {
		note(fmt.Sprintf("an object named %q exists without this resource's markers, so it is not bound", name))
		return ""
	}
	note(fmt.Sprintf("bound %q by a read of its name", name))
	b.ledger.record(req.URN, func(e *ownershipEntry) { e.Bound = resp.ID })
	return resp.ID
}

// info0 reports a binder diagnostic through the engine's event stream, when the request carries its sink.
func info0(req deploy.BindRequest, format string, args ...any) {
	if req.Diag != nil {
		req.Diag.Infof(diag.Message(req.URN, format), args...)
	}
}

func mustMarkers(urn resource.URN, info TypeInfo) map[string]string {
	m, err := markers.For(urn, info.Encoding())
	if err != nil {
		return map[string]string{markers.TagStack: "\x00unrepresentable"}
	}
	return m
}

// providerString reads a string-valued provider input.
func providerString(inputs resource.PropertyMap, key string) string {
	v, ok := inputs[resource.PropertyKey(key)]
	if !ok {
		return ""
	}
	v = unwrapValue(v)
	if v.IsString() {
		return v.StringValue()
	}
	return ""
}

// filterMarked keeps the objects carrying want.
func filterMarked(objs []LiveObject, want map[string]string) []LiveObject {
	var out []LiveObject
	for _, o := range objs {
		if markers.Carries(o.Tags, want) {
			out = append(out, o)
		}
	}
	return out
}

// listerCache is a keyed cache of per-configuration clients, built once.
type listerCache[T any] struct {
	mu    sync.Mutex
	items map[string]*T
}

func (c *listerCache[T]) get(key string, build func() (*T, error)) (*T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]*T{}
	}
	if v, ok := c.items[key]; ok {
		return v, nil
	}
	v, err := build()
	if err != nil {
		return nil, err
	}
	c.items[key] = v
	return v, nil
}
