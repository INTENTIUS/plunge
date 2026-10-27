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
	"errors"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// The JSON view of an adoption, printed by `plunge stack adopt --json` (INTENTIUS/plunge#17). The shape is
// documented in docs/plunge/ADOPT.md; a change to it is a new AdoptViewVersion.

// AdoptViewVersion is the version of the document, in its "adopt" field.
const AdoptViewVersion = "v1"

// AdoptStatus is what the adoption did, or would do.
type AdoptStatus string

const (
	// AdoptWouldAdopt is a classified, unconfirmed adoption: nothing was written.
	AdoptWouldAdopt AdoptStatus = "would-adopt"
	// AdoptAdopted is an adoption this call wrote.
	AdoptAdopted AdoptStatus = "adopted"
	// AdoptAlreadyAdopted is a stack whose adoption record names this checkpoint: nothing was written.
	AdoptAlreadyAdopted AdoptStatus = "already-adopted"
	// AdoptRefused is a refused adoption: nothing was written, and the command exits non-zero.
	AdoptRefused AdoptStatus = "refused"
)

// AdoptFate is one resource's fate in the view: its ownership class, or already-adopted.
type AdoptFate string

const (
	AdoptFateMarker         AdoptFate = AdoptFate(OwnershipMarker)
	AdoptFateRecord         AdoptFate = AdoptFate(OwnershipRecord)
	AdoptFatePending        AdoptFate = AdoptFate(OwnershipPending)
	AdoptFateAlreadyAdopted AdoptFate = "already-adopted"
)

// AdoptView is the `plunge stack adopt --json` document.
type AdoptView struct {
	Adopt      string              `json:"adopt"`
	Stack      string              `json:"stack"`
	Status     AdoptStatus         `json:"status"`
	Checkpoint AdoptViewCheckpoint `json:"checkpoint"`
	// Index is the index version an adoption from an adoption file read (--adopt); absent for a checkpoint.
	Index     *AdoptViewIndex     `json:"index,omitempty"`
	Resources []AdoptViewResource `json:"resources"`
	Refusals  []AdoptViewRefusal  `json:"refusals"`
}

// AdoptViewCheckpoint is the checkpoint the adoption read.
type AdoptViewCheckpoint struct {
	Source string `json:"source,omitempty"`
	Digest string `json:"digest,omitempty"`
}

// AdoptViewIndex is the adoption file and the index version it names.
type AdoptViewIndex struct {
	File     string `json:"file,omitempty"`
	ScopeKey string `json:"scopeKey"`
	Version  string `json:"version"`
	Digest   string `json:"digest"`
}

// AdoptViewMarkerWrite is the one write the first `plunge up` makes to an object adopted from an index: these
// markers set in Property. TagsOnly is always true; it says, for a reader of the document, that the write changes
// the tags (or labels) and nothing else (choudoufu#2284 Q3).
type AdoptViewMarkerWrite struct {
	Property string            `json:"property"`
	Tags     map[string]string `json:"tags"`
	TagsOnly bool              `json:"tagsOnly"`
}

// AdoptViewResource is one resource of the checkpoint, or one object of an index adoption.
type AdoptViewResource struct {
	URN  resource.URN `json:"urn"`
	Type string       `json:"type"`
	ID   resource.ID  `json:"id"`
	Fate AdoptFate    `json:"fate"`
	// AdoptedAs is the ownership class the adoption record holds, for an already-adopted resource.
	AdoptedAs Ownership `json:"adoptedAs,omitempty"`
	// Marked reports that the checkpoint's inputs already carry this stack's markers.
	Marked bool `json:"marked"`
	// Reason says why the fate is record or pending.
	Reason string `json:"reason,omitempty"`
	// Node is the index node an index adoption adopts as this resource, and ImportID the ID it is imported by.
	Node     string      `json:"node,omitempty"`
	ImportID resource.ID `json:"importId,omitempty"`
	// MarkerWrite is the marker write the first `plunge up` makes; absent when the object already carries its
	// markers, for a record-owned type, and for a checkpoint adoption.
	MarkerWrite *AdoptViewMarkerWrite `json:"markerWrite,omitempty"`
	// Refusals are the refusals that name this resource.
	Refusals []AdoptViewRefusal `json:"refusals,omitempty"`
}

// AdoptViewRefusal is one reason the adoption was refused.
type AdoptViewRefusal struct {
	Code   AdoptRefusalCode `json:"code"`
	URN    resource.URN     `json:"urn,omitempty"`
	Reason string           `json:"reason"`
}

// NewAdoptView is the view of one Adopt call's result and error. An error other than a refusal or the
// confirmation gate is returned as it is: it is not an adoption outcome.
func NewAdoptView(stack string, res *AdoptResult, err error) (*AdoptView, error) {
	v := &AdoptView{Adopt: AdoptViewVersion, Stack: stack, Resources: []AdoptViewResource{},
		Refusals: []AdoptViewRefusal{}}
	var unconfirmed *AdoptUnconfirmedError
	var refused *AdoptRefusedError
	switch {
	case err == nil && res != nil && res.AlreadyAdopted:
		v.Status = AdoptAlreadyAdopted
	case err == nil && res != nil && res.Written:
		v.Status = AdoptAdopted
	case err == nil && res != nil:
		v.Status = AdoptWouldAdopt
	case errors.As(err, &unconfirmed):
		v.Status, res = AdoptWouldAdopt, unconfirmed.Result
	case errors.As(err, &refused):
		v.Status, res = AdoptRefused, refused.Result
		for _, r := range refused.Refusals {
			v.Refusals = append(v.Refusals, AdoptViewRefusal{Code: r.Code, URN: r.URN, Reason: r.Reason})
		}
	case err != nil:
		return nil, err
	default:
		return nil, errors.New("plunge: an adoption returned neither a result nor an error")
	}
	if res == nil {
		return v, nil
	}
	v.Checkpoint = AdoptViewCheckpoint{Source: res.Source, Digest: res.Digest}
	if res.Index != nil {
		v.Index = &AdoptViewIndex{File: res.Index.File, ScopeKey: res.Index.ScopeKey, Version: res.Index.Version,
			Digest: res.Index.Digest}
	}
	for _, r := range res.Resources {
		e := AdoptViewResource{URN: r.URN, Type: string(r.Type), ID: r.ID, Fate: AdoptFate(r.Ownership),
			Marked: r.Marked, Reason: r.Reason, Node: r.Node, ImportID: r.ImportID}
		if r.MarkerWrite != nil && !res.AlreadyAdopted {
			e.MarkerWrite = &AdoptViewMarkerWrite{Property: r.MarkerWrite.Property, Tags: r.MarkerWrite.Tags,
				TagsOnly: true}
		}
		if res.AlreadyAdopted {
			e.Fate, e.AdoptedAs = AdoptFateAlreadyAdopted, r.Ownership
		}
		for _, f := range v.Refusals {
			if f.URN == r.URN {
				e.Refusals = append(e.Refusals, f)
			}
		}
		v.Resources = append(v.Resources, e)
	}
	return v, nil
}
