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

package apitype

import "github.com/pulumi/pulumi/sdk/v3/go/common/resource"

// PlungeTimingKind says which side of a resource step a timed call was made to.
type PlungeTimingKind string

const (
	// PlungeTimingProvider is a call to the resource's provider.
	PlungeTimingProvider PlungeTimingKind = "provider"
	// PlungeTimingRecord is a call to the plunge record store for the resource's record.
	PlungeTimingRecord PlungeTimingKind = "record"
)

// PlungeTiming is one call plunge timed for a resource during an update (INTENTIUS/plunge#55). It is carried in
// StepEventMetadata.PlungeTimings on resource-outputs and resource-operation-failed events, on the plunge backend
// only. Every other backend leaves the list empty, so the field is absent from their events.
type PlungeTiming struct {
	// Kind is "provider" or "record".
	Kind PlungeTimingKind `json:"kind"`
	// Call is the provider method for a provider call ("check", "diff", "create", "read", "update", "delete"), or
	// the record-store call for a record ("write", "delete").
	Call string `json:"call"`
	// StartedAt is when the call began, in RFC 3339 with nanoseconds, UTC.
	StartedAt string `json:"startedAt"`
	// DurationMs is how long the call took, in milliseconds.
	DurationMs float64 `json:"durationMs"`
	// Failed is set when the call returned an error.
	Failed bool `json:"failed,omitempty"`
	// URN names the resource the call was for when it is not the event's own. A default provider's step event is
	// internal and not shown, so its record write rides on the first event of a resource that uses the provider,
	// with the provider's URN here. Empty for the event's own resource.
	URN resource.URN `json:"urn,omitempty"`
}
