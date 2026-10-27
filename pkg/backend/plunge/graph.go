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

// The stack graph emitter behind `plunge graph --json` (INTENTIUS/plunge#40). It prints one engine graph document
// in the shape chant#4520 designed and chant#4527's schema checks: a GraphIR at version 1 whose meta.engineGraph says
// what was read, one node per URN with the engine's facts nested in `facts`, and `ref` edges with their grain.
//
// The read is the stack's records in one bulk read and nothing else: no preview, no provider call, no write. Because
// no live object is read, markers carry `source: "record"` (what the record says was written) and ownership is only
// ever bound or pending (chant#4520 ruling, Q5). Output values never reach the document: the only outputs read are a
// fixed per-family list of ID-shaped ones (plunge#38 ruling, Q4), and an output that is a secret envelope is skipped
// without being decrypted. The secrets record is never read.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/sig"
)

// GraphDocument is chant's engine graph document (chant#4520 section 3, `EngineGraphDocument` in chant#4527's
// schema).
type GraphDocument struct {
	Version int         `json:"version"`
	Meta    GraphMeta   `json:"meta"`
	Nodes   []GraphNode `json:"nodes"`
	Edges   []GraphEdge `json:"edges"`
	Groups  struct{}    `json:"groups"`
}

// GraphMeta is the document's meta bag; plunge writes only engineGraph.
type GraphMeta struct {
	EngineGraph EngineGraph `json:"engineGraph"`
}

// EngineGraph is meta.engineGraph: which engine read what, when, and with which marker scheme.
type EngineGraph struct {
	Format       int               `json:"format"`
	Engine       GraphEngine       `json:"engine"`
	Scope        GraphScope        `json:"scope"`
	ObservedAt   string            `json:"observedAt,omitempty"`
	MarkerScheme GraphMarkerScheme `json:"markerScheme"`
	Gaps         []GraphGap        `json:"gaps,omitempty"`
}

// GraphEngine names the engine that printed the document.
type GraphEngine struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// GraphScope is what the read covered: one stack of one project.
type GraphScope struct {
	Kind    string `json:"kind"`
	Project string `json:"project"`
	Stack   string `json:"stack"`
}

// GraphMarkerScheme is the marker keys and encodings plunge writes, printed once so a reader can tell a digest from
// a value without knowing the engine.
type GraphMarkerScheme struct {
	OwnerKey         string        `json:"ownerKey"`
	AddressKey       string        `json:"addressKey"`
	ContinuationKeys []string      `json:"continuationKeys,omitempty"`
	Encoding         GraphEncoding `json:"encoding"`
}

// GraphEncoding is how the tag families and the label families carry markers.
type GraphEncoding struct {
	Tags   string `json:"tags"`
	Labels string `json:"labels"`
}

// GraphGap is a record the emitter could not describe, and why. Detail never holds a record value.
type GraphGap struct {
	Address string `json:"address"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail,omitempty"`
}

// GraphNode is one URN. Attrs carry only the address: no inputs and no outputs.
type GraphNode struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Lexicon    string            `json:"lexicon"`
	Attrs      map[string]string `json:"attrs"`
	PhysicalID string            `json:"physicalId,omitempty"`
	Facts      *EngineFacts      `json:"facts,omitempty"`
}

// EngineFacts is what plunge's records say about one node (chant#4520 section 2). Never read for a decision.
type EngineFacts struct {
	Ownership      string               `json:"ownership,omitempty"`
	Markers        *GraphMarkers        `json:"markers,omitempty"`
	OwnedBy        string               `json:"ownedBy,omitempty"`
	CloudIDs       []GraphCloudID       `json:"cloudIds,omitempty"`
	RecordVersions *GraphRecordVersions `json:"recordVersions,omitempty"`
	ObservedAt     string               `json:"observedAt,omitempty"`
}

// GraphMarkers is the marker values the record says plunge wrote on the object.
type GraphMarkers struct {
	Owner    string `json:"owner,omitempty"`
	Address  string `json:"address,omitempty"`
	Encoding string `json:"encoding"`
	Source   string `json:"source"`
}

// GraphCloudID is one cloud identifier of a node.
type GraphCloudID struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// GraphRecordVersions is facts.recordVersions (`--versions`, INTENTIUS/plunge#43). The emitter of #40 never sets it.
type GraphRecordVersions struct {
	Kept     bool                 `json:"kept"`
	Versions []GraphRecordVersion `json:"versions"`
}

// GraphRecordVersion is one version of a node's record, with no record value.
type GraphRecordVersion struct {
	ID        string `json:"id"`
	WrittenAt string `json:"writtenAt"`
	Current   bool   `json:"current"`
	Deleted   bool   `json:"deleted"`
}

// GraphEdge is a `ref` edge from the URN that holds the reference to the URN it names.
type GraphEdge struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Kind    string `json:"kind"`
	ViaAttr string `json:"viaAttr,omitempty"`
	Grain   string `json:"grain,omitempty"`
}

// The edge channels and grains (chant#4520 section 3).
const (
	GrainInstance = "instance"
	GrainProperty = "property"

	ViaDependencies = "dependencies"
	ViaParent       = "parent"
	ViaProvider     = "provider"
)

// GraphRequest is one emit.
type GraphRequest struct {
	// EngineVersion is plunge's own version, printed as meta.engineGraph.engine.version when set.
	EngineVersion string
	// Now is when the read starts; time.Now when nil.
	Now func() time.Time
}

// EmitGraph reads a stack's records in one bulk read and returns its engine graph document. It makes no provider
// call and writes no record.
func EmitGraph(ctx context.Context, be backend.Backend, ref backend.StackReference, req GraphRequest) (*GraphDocument, error) {
	pb, ok := be.(*plungeBackend)
	if !ok {
		return nil, &NotPlungeGraphError{URL: be.URL()}
	}
	keys, err := keysFor(ref)
	if err != nil {
		return nil, err
	}
	project, _ := ref.Project()
	return emitGraph(ctx, pb.store, keys, string(project), ref.Name().String(), req)
}

// NotPlungeGraphError refuses `plunge graph` on a backend that keeps no records per resource.
type NotPlungeGraphError struct {
	URL string
}

func (e *NotPlungeGraphError) Error() string {
	return fmt.Sprintf("plunge: `plunge graph` reads the plunge backend's records (%sfile://, %ss3://, ...); "+
		"the current backend is %s", URLPrefix, URLPrefix, e.URL)
}

func emitGraph(
	ctx context.Context, store staterecord.Store, keys stackKeys, project, stack string, req GraphRequest,
) (*GraphDocument, error) {
	now := time.Now
	if req.Now != nil {
		now = req.Now
	}
	observedAt := now().UTC().Format(time.RFC3339)

	raw, err := readAll(ctx, store, keys)
	if err != nil {
		return nil, fmt.Errorf("plunge: reading the records under %q: %w", keys.prefix(), err)
	}

	doc := &GraphDocument{
		Version: 1,
		Meta: GraphMeta{EngineGraph: EngineGraph{
			Format:       1,
			Engine:       GraphEngine{Name: "plunge", Version: req.EngineVersion},
			Scope:        GraphScope{Kind: "stack", Project: project, Stack: stack},
			ObservedAt:   observedAt,
			MarkerScheme: plungeMarkerScheme(),
		}},
		Nodes: []GraphNode{},
		Edges: []GraphEdge{},
	}

	sortedKeys := make([]string, 0, len(raw))
	for k := range raw {
		if strings.HasPrefix(k, keys.resourcePrefix()) {
			sortedKeys = append(sortedKeys, k)
		}
	}
	sort.Strings(sortedKeys)

	edges := map[GraphEdge]bool{}
	for _, key := range sortedKeys {
		urn, ok := decodeSegment(key, keys.resourcePrefix())
		if !ok {
			doc.Meta.EngineGraph.Gaps = append(doc.Meta.EngineGraph.Gaps, GraphGap{
				Address: key, Reason: "unreadable-key", Detail: "the key does not name a URN this version writes",
			})
			continue
		}
		var r resourceRecord
		if err := decodeRecord(key, raw[key].Payload, resourceFormat, &r); err != nil {
			doc.Meta.EngineGraph.Gaps = append(doc.Meta.EngineGraph.Gaps, GraphGap{
				Address: urn, Reason: "unreadable-record", Detail: "the record is not a " + resourceFormat + " record",
			})
			continue
		}
		if string(r.URN) != urn {
			doc.Meta.EngineGraph.Gaps = append(doc.Meta.EngineGraph.Gaps, GraphGap{
				Address: urn, Reason: "mismatched-record", Detail: "the record names another URN than its key",
			})
			continue
		}
		node, state, ok := graphNode(&r, observedAt)
		if !ok {
			doc.Meta.EngineGraph.Gaps = append(doc.Meta.EngineGraph.Gaps, GraphGap{
				Address: urn, Reason: "no-state", Detail: "the record holds no state and no pending create",
			})
			continue
		}
		doc.Nodes = append(doc.Nodes, node)
		for _, e := range stateEdges(state) {
			edges[e] = true
		}
	}

	for e := range edges {
		doc.Edges = append(doc.Edges, e)
	}
	sort.Slice(doc.Edges, func(i, j int) bool {
		a, b := doc.Edges[i], doc.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Grain != b.Grain {
			return a.Grain < b.Grain
		}
		return a.ViaAttr < b.ViaAttr
	})
	return doc, nil
}

// plungeMarkerScheme is the scheme pkg/backend/plunge/markers writes: plain values on the tag families, digests on
// the label families.
func plungeMarkerScheme() GraphMarkerScheme {
	cont := make([]string, 0, markers.MaxContinuations-1)
	for i := 1; i < markers.MaxContinuations; i++ {
		cont = append(cont, markers.ContinuationKey(i))
	}
	return GraphMarkerScheme{
		OwnerKey:         markers.TagStack,
		AddressKey:       markers.TagURN,
		ContinuationKeys: cont,
		Encoding:         GraphEncoding{Tags: "plain", Labels: "digest"},
	}
}

// graphNode is the node for one record and the state its edges come from: the live state when there is one, else
// the first pending delete, else a pending create's state. ok is false for a record with none of them.
func graphNode(r *resourceRecord, observedAt string) (GraphNode, apitype.ResourceV3, bool) {
	var state apitype.ResourceV3
	var ownership string
	var found bool
	if live, ok := liveState(r); ok {
		state, ownership, found = live, "bound", true
	} else if len(r.States) > 0 {
		// Only pending deletes: the objects still exist and the record still holds them.
		state, ownership, found = r.States[0], "bound", true
	} else {
		for _, op := range r.Pending {
			if op.Type == apitype.OperationTypeCreating {
				state, ownership, found = op.Resource, "pending", true
				break
			}
		}
	}
	if !found {
		return GraphNode{}, apitype.ResourceV3{}, false
	}

	node := GraphNode{
		ID:      string(r.URN),
		Kind:    string(state.Type),
		Lexicon: "pulumi",
		Attrs:   map[string]string{"address": string(r.URN)},
	}
	facts := &EngineFacts{ObservedAt: observedAt}
	// A component, or the root Stack, has no cloud object, so the record cannot bind one.
	if state.Custom {
		facts.Ownership = ownership
	}
	if ownership == "bound" && state.ID != "" {
		node.PhysicalID = string(state.ID)
	}
	if e := r.Ownership; e != nil {
		switch e.By {
		case OwnershipMarker, OwnershipRecord:
			facts.OwnedBy = string(e.By)
		}
		if e.By == OwnershipMarker {
			facts.Markers = recordMarkers(e, state)
		}
	}
	if node.PhysicalID != "" {
		facts.CloudIDs = cloudIDs(state)
	}
	node.Facts = facts
	return node, state, true
}

// recordMarkers is the marker values an ownership entry says were written. Nil when it names none.
func recordMarkers(e *ownershipEntry, state apitype.ResourceV3) *GraphMarkers {
	if len(e.Marker) == 0 {
		return nil
	}
	owner, address, ok := markers.Owner(e.Marker)
	if !ok {
		return nil
	}
	family := e.Family
	if family == FamilyOther {
		family = familyOf(state.Type)
	}
	encoding := "plain"
	if family.encoding() == markers.EncodingLabels {
		encoding = "digest"
	}
	return &GraphMarkers{Owner: owner, Address: address, Encoding: encoding, Source: "record"}
}

// cloudIDOutputs is the fixed per-family list of ID-shaped outputs (plunge#38 ruling, Q4): a path into the outputs
// and the cloudIds kind it is.
var cloudIDOutputs = map[Family][]struct {
	path []string
	kind string
}{
	FamilyAWS:         {{[]string{"arn"}, "arn"}},
	FamilyAWSNative:   {{[]string{"arn"}, "arn"}},
	FamilyGoogle:      {{[]string{"selfLink"}, "self-link"}, {[]string{"id"}, "provider-id"}},
	FamilyAzure:       {{[]string{"id"}, "provider-id"}},
	FamilyAzureNative: {{[]string{"id"}, "provider-id"}},
	FamilyKubernetes:  {{[]string{"metadata", "uid"}, "k8s-uid"}},
}

// cloudIDs is the state's ID as the primary cloud ID, then the family's ID-shaped outputs that are plain strings.
// A secret output, or one with a secret on its path, is skipped and never decrypted; so is an output the state
// lists as an additional secret output.
func cloudIDs(state apitype.ResourceV3) []GraphCloudID {
	out := []GraphCloudID{{Kind: "provider-id", Value: string(state.ID)}}
	seen := map[GraphCloudID]bool{out[0]: true}
	// A provider-id output equal to the state's ID adds nothing.
	seenValue := map[string]bool{string(state.ID): true}
	secretOut := map[string]bool{}
	for _, k := range state.AdditionalSecretOutputs {
		secretOut[string(k)] = true
	}
	for _, o := range cloudIDOutputs[familyOf(state.Type)] {
		if secretOut[o.path[0]] {
			continue
		}
		v, ok := plainString(state.Outputs, o.path)
		if !ok {
			continue
		}
		id := GraphCloudID{Kind: o.kind, Value: v}
		if seen[id] || (o.kind == "provider-id" && seenValue[v]) {
			continue
		}
		seen[id] = true
		seenValue[v] = true
		out = append(out, id)
	}
	return out
}

// plainString walks path through outputs and returns the string at its end, when every step is a plain object and
// the end a plain, known string. Any secret envelope (or other signed value) on the way stops the walk.
func plainString(outputs map[string]any, path []string) (string, bool) {
	var cur any = outputs
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok || isSigned(m) {
			return "", false
		}
		cur, ok = m[p]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	if !ok || s == "" || s == sig.UnknownStringValue {
		return "", false
	}
	return s, true
}

// isSigned reports whether m is a signed value (a secret, an asset, a resource reference) rather than a plain object.
func isSigned(m map[string]any) bool {
	_, ok := m[sig.Key]
	return ok
}

// stateEdges is the `ref` edges a state holds: its dependencies, parent and provider at instance grain, and each
// property dependency at property grain, named by the property.
func stateEdges(state apitype.ResourceV3) []GraphEdge {
	from := string(state.URN)
	var out []GraphEdge
	add := func(to resource.URN, via, grain string) {
		if to == "" {
			return
		}
		out = append(out, GraphEdge{From: from, To: string(to), Kind: "ref", ViaAttr: via, Grain: grain})
	}
	for _, dep := range state.Dependencies {
		add(dep, ViaDependencies, GrainInstance)
	}
	add(state.Parent, ViaParent, GrainInstance)
	if state.Provider != "" {
		if ref, err := providers.ParseReference(state.Provider); err == nil {
			add(ref.URN(), ViaProvider, GrainInstance)
		}
	}
	for prop, deps := range state.PropertyDependencies {
		for _, dep := range deps {
			add(dep, string(prop), GrainProperty)
		}
	}
	return out
}
