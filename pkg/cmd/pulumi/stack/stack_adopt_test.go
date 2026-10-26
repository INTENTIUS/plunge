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

package stack

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/result"
)

type fakeAdopter struct {
	calls []plunge.AdoptOptions
	res   *plunge.AdoptResult
	err   error
}

func (f *fakeAdopter) Adopt(_ context.Context, _ backend.Stack, opts plunge.AdoptOptions) (*plunge.AdoptResult, error) {
	f.calls = append(f.calls, opts)
	return f.res, f.err
}

func adoptTestStack() backend.Stack {
	return &backend.MockStack{RefF: func() backend.StackReference {
		return &backend.MockStackReference{StringV: "organization/test/dev"}
	}}
}

// --json alone calls Adopt once, unconfirmed, and prints the view; a refusal is the document and a bail.
func TestStackAdoptJSON(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	res := &plunge.AdoptResult{Digest: "abc", Source: "the file s.json"}
	a := &fakeAdopter{err: &plunge.AdoptUnconfirmedError{Stack: "organization/test/dev", Result: res}}
	var out bytes.Buffer
	require.NoError(t, runStackAdoptJSON(ctx, &out, a, adoptTestStack(), plunge.AdoptOptions{}, false))
	require.Len(t, a.calls, 1)
	assert.False(t, a.calls[0].Confirmed, "--json without --yes never confirms")
	var doc map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	assert.Equal(t, "v1", doc["adopt"])
	assert.Equal(t, "would-adopt", doc["status"])

	refused := &fakeAdopter{err: &plunge.AdoptRefusedError{
		Stack:    "organization/test/dev",
		Reasons:  []string{"pending"},
		Refusals: []plunge.AdoptRefusal{{Code: plunge.RefusePendingOperations, Reason: "pending"}},
	}}
	out.Reset()
	err := runStackAdoptJSON(ctx, &out, refused, adoptTestStack(), plunge.AdoptOptions{}, false)
	require.Error(t, err)
	assert.True(t, result.IsBail(err), "the refusal is printed once, as the document")
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	assert.Equal(t, "refused", doc["status"])
	assert.Len(t, doc["refusals"], 1)

	// With --yes the adoption is confirmed.
	yes := &fakeAdopter{res: &plunge.AdoptResult{Written: true}}
	out.Reset()
	require.NoError(t, runStackAdoptJSON(ctx, &out, yes, adoptTestStack(), plunge.AdoptOptions{}, true))
	assert.True(t, yes.calls[0].Confirmed)
}
