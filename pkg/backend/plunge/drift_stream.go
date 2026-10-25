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

// The drift stream is chant's drift record v1 NDJSON (chant packages/core/src/drift-record.ts, decodeDriftLine), in
// the layout choudoufu's `live-plan -resource` writes (choudoufu internal/command/views/live_drift.go): one JSON
// object per line, each with a "type":
//
//	{"type":"drift-start", ...}   once, naming the one resource the read expects
//	{"type":"drift-record", ...}  the resource's record
//	{"type":"drift-settle", ...}  once, last, with the counts and what the read cost

import (
	"encoding/json"
	"fmt"
	"io"
)

// DriftStartLine is the drift-start line.
type DriftStartLine struct {
	Type     string        `json:"type"`
	Drift    string        `json:"drift"`
	Member   string        `json:"member"`
	Engine   string        `json:"engine"`
	Read     string        `json:"read"`
	Resource string        `json:"resource,omitempty"`
	At       string        `json:"at,omitempty"`
	Expected []string      `json:"expected"`
	Evidence DriftEvidence `json:"evidence"`
}

// DriftRecordLine is one drift-record line: the record's fields with "type" beside them.
type DriftRecordLine struct {
	Type string `json:"type"`
	DriftRecord
}

// DriftSettleLine is the drift-settle line.
type DriftSettleLine struct {
	Type   string         `json:"type"`
	Drift  string         `json:"drift"`
	Member string         `json:"member"`
	Engine string         `json:"engine"`
	Read   string         `json:"read"`
	Counts map[string]int `json:"counts"`
	Reads  DriftReads     `json:"reads"`
	At     string         `json:"at,omitempty"`
}

// DriftLines are the three lines of one resource's drift stream.
func DriftLines(res *DriftResult) (DriftStartLine, DriftRecordLine, DriftSettleLine) {
	r := res.Record
	start := DriftStartLine{
		Type:     "drift-start",
		Drift:    DriftVersion,
		Member:   r.Member,
		Engine:   DriftEngine,
		Read:     DriftRead,
		Resource: r.Address,
		Expected: []string{r.Address},
		Evidence: r.Evidence,
	}
	settle := DriftSettleLine{
		Type:   "drift-settle",
		Drift:  DriftVersion,
		Member: r.Member,
		Engine: DriftEngine,
		Read:   DriftRead,
		Counts: map[string]int{r.Status: 1},
		Reads:  res.Reads,
		At:     r.ObservedAt,
	}
	return start, DriftRecordLine{Type: "drift-record", DriftRecord: r}, settle
}

// WriteDriftStream writes one resource's drift stream to w.
func WriteDriftStream(w io.Writer, res *DriftResult) error {
	start, record, settle := DriftLines(res)
	for _, line := range []any{start, record, settle} {
		b, err := json.Marshal(line)
		if err != nil {
			return fmt.Errorf("plunge: encoding a drift line: %w", err)
		}
		if _, err := fmt.Fprintln(w, string(b)); err != nil {
			return err
		}
	}
	return nil
}
