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

// The `plunge stack adopt --json` view (INTENTIUS/plunge#17). Written for this unit and not run in it.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// The view of an unconfirmed adoption (--json alone) classes every resource and writes nothing: no adoption record
// and no store write at all.
func TestAdoptViewWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	f := newAdoptFixture(t, stockDeployment(t, false))
	res, err := f.backend.Adopt(ctx, f.stack, AdoptOptions{})
	view, verr := NewAdoptView(f.stack.Ref().String(), res, err)
	require.NoError(t, verr)

	assert.Zero(t, f.store.total(), "--json alone writes nothing")
	l, err := load(ctx, f.store, mustKeys(t, f.stack))
	require.NoError(t, err)
	assert.Nil(t, l.adoption, "no adoption record")

	assert.Equal(t, "v1", view.Adopt)
	assert.Equal(t, AdoptWouldAdopt, view.Status)
	assert.Equal(t, f.stack.Ref().String(), view.Stack)
	assert.NotEmpty(t, view.Checkpoint.Digest)
	assert.Contains(t, view.Checkpoint.Source, "DIY checkpoint")
	assert.Empty(t, view.Refusals)
	require.Len(t, view.Resources, 1)
	r := view.Resources[0]
	assert.Equal(t, bucketURN(), r.URN)
	assert.Equal(t, string(bucketType), r.Type)
	assert.Equal(t, resource.ID("logs"), r.ID)
	assert.Equal(t, AdoptFatePending, r.Fate, "a bridged type is classed at the first run")
	assert.NotEmpty(t, r.Reason)

	raw, err := json.Marshal(view)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, "v1", doc["adopt"])
	assert.Equal(t, "would-adopt", doc["status"])
	assert.Equal(t, []any{}, doc["refusals"], "refusals is an empty list, not null")
}

// A refused adoption is a document: the refusals with their codes, each also on the resource it names, and the
// classification it was reached from. Nothing is written.
func TestAdoptViewOfARefusal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	f := newAdoptFixture(t, stockDeployment(t, true))
	res, err := f.backend.Adopt(ctx, f.stack, AdoptOptions{})
	require.Error(t, err)
	view, verr := NewAdoptView(f.stack.Ref().String(), res, err)
	require.NoError(t, verr)
	assert.Zero(t, f.store.total())

	assert.Equal(t, AdoptRefused, view.Status)
	require.Len(t, view.Refusals, 1)
	svc := resource.NewURN("test", "test", "", "aws-native:apprunner:Service", "svc")
	assert.Equal(t, RefuseCreateOnlyTags, view.Refusals[0].Code)
	assert.Equal(t, svc, view.Refusals[0].URN)
	assert.Contains(t, view.Refusals[0].Reason, "aws-native:apprunner:Service")

	var found bool
	for _, r := range view.Resources {
		if r.URN != svc {
			assert.Empty(t, r.Refusals)
			continue
		}
		found = true
		assert.Equal(t, AdoptFateMarker, r.Fate)
		require.Len(t, r.Refusals, 1)
		assert.Equal(t, RefuseCreateOnlyTags, r.Refusals[0].Code)
	}
	assert.True(t, found, "the refused resource is in the document")
}

// After an adoption, the view of the same checkpoint says already-adopted, with each resource's adopted class.
func TestAdoptViewOfAnAdoptedStack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	f := newAdoptFixture(t, stockDeployment(t, false))
	res, err := f.backend.Adopt(ctx, f.stack, AdoptOptions{Confirmed: true})
	require.NoError(t, err)
	view, err := NewAdoptView(f.stack.Ref().String(), res, nil)
	require.NoError(t, err)
	assert.Equal(t, AdoptAdopted, view.Status)

	before := f.store.total()
	res, err = f.backend.Adopt(ctx, f.stack, AdoptOptions{})
	require.NoError(t, err)
	view, err = NewAdoptView(f.stack.Ref().String(), res, nil)
	require.NoError(t, err)
	assert.Equal(t, before, f.store.total())
	assert.Equal(t, AdoptAlreadyAdopted, view.Status)
	require.Len(t, view.Resources, 1)
	assert.Equal(t, AdoptFateAlreadyAdopted, view.Resources[0].Fate)
	assert.Equal(t, OwnershipPending, view.Resources[0].AdoptedAs)
}
