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

// The acceptance tests of INTENTIUS/plunge#37, against the local record store and the in-process engine, with a
// deploytest provider standing in for pulumi-aws and an in-memory map standing in for the cloud. No emulator and
// no cloud: these tests delete (or refuse to delete) another stack's object, so they run on fakes only.
//
//   - red first: without the vet, `up` and `destroy` delete an object whose live marker names another stack;
//   - with it, `up` and `destroy` refuse the run, name the resource, the stack the marker names and the record,
//     and the object and its record survive; the same for a taggable object whose marker is missing;
//   - a record-owned type (no tags) still deletes, its record's ownership entry says why, and no read is made;
//   - with refresh on, the vet makes no provider call beyond the refresh; with it off, one read per marked object.
//
// Written for plunge#37 and not run in that unit.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/engine"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

const policyType = "aws:s3/bucketPolicy:BucketPolicy"

// vetMapping is a bridged mapping with a taggable bucket and a bucket policy, which has no tags argument and is
// record-owned.
const vetMapping = `{
  "provider": {"resources": {
    "aws_s3_bucket": {"bucket": {"type": 4, "optional": true}, "tags": {"type": 6, "optional": true, "element": {"schema": {"type": 4}}}},
    "aws_s3_bucket_policy": {"bucket": {"type": 4, "optional": true}, "policy": {"type": 4, "optional": true}}
  }},
  "resources": {
    "aws_s3_bucket": {"tok": "aws:s3/bucket:Bucket", "fields": {}},
    "aws_s3_bucket_policy": {"tok": "aws:s3/bucketPolicy:BucketPolicy", "fields": {}}
  }
}`

// vetCloud is the live side: what a Read returns, and what was deleted. A retagged object reads back with the
// tags set here in place of the ones last written.
type vetCloud struct {
	mu      sync.Mutex
	reads   int
	deleted []resource.ID
	retags  map[resource.ID]map[string]string
}

func newVetCloud() *vetCloud { return &vetCloud{retags: map[resource.ID]map[string]string{}} }

func (c *vetCloud) retag(id resource.ID, tags map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retags[id] = tags
}

func (c *vetCloud) resetReads() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads = 0
}

func (c *vetCloud) readCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *vetCloud) wasDeleted(id resource.ID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, d := range c.deleted {
		if d == id {
			return true
		}
	}
	return false
}

func (c *vetCloud) loaders() []*deploytest.ProviderLoader {
	return []*deploytest.ProviderLoader{
		deploytest.NewProviderLoader("aws", semver.MustParse("7.0.0"), func() (plugin.Provider, error) {
			return c.provider(), nil
		}),
	}
}

func (c *vetCloud) provider() *deploytest.Provider {
	return &deploytest.Provider{
		Version: semver.MustParse("7.0.0"),
		GetMappingF: func(_ context.Context, req plugin.GetMappingRequest) (plugin.GetMappingResponse, error) {
			if req.Key != "terraform" {
				return plugin.GetMappingResponse{}, nil
			}
			return plugin.GetMappingResponse{Provider: "aws", Data: []byte(vetMapping)}, nil
		},
		CreateF: func(_ context.Context, req plugin.CreateRequest) (plugin.CreateResponse, error) {
			return plugin.CreateResponse{
				ID: resource.ID(req.URN.Name() + "-id"), Properties: req.Properties, Status: resource.StatusOK,
			}, nil
		},
		UpdateF: func(_ context.Context, req plugin.UpdateRequest) (plugin.UpdateResponse, error) {
			return plugin.UpdateResponse{Properties: req.NewInputs, Status: resource.StatusOK}, nil
		},
		ReadF: func(_ context.Context, req plugin.ReadRequest) (plugin.ReadResponse, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.reads++
			outputs := resource.ToResourcePropertyMap(req.State).Copy()
			if tags, ok := c.retags[req.ID]; ok {
				outputs["tags"] = resource.NewProperty(resource.NewPropertyMapFromMap(toAny(tags)))
			}
			out := resource.FromResourcePropertyMap(outputs)
			in := req.Inputs
			return plugin.ReadResponse{
				ReadResult: plugin.ReadResult{ID: req.ID, Outputs: &out, Inputs: &in},
				Status:     resource.StatusOK,
			}, nil
		},
		DeleteF: func(_ context.Context, req plugin.DeleteRequest) (plugin.DeleteResponse, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.deleted = append(c.deleted, req.ID)
			return plugin.DeleteResponse{Status: resource.StatusOK}, nil
		},
	}
}

func toAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// vetProgram registers the bucket and the policy while *registered is true, and nothing after.
func vetProgram(registered *bool) deploytest.LanguageRuntimeFactory {
	return deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		if !*registered {
			return nil
		}
		if _, err := monitor.RegisterResource(bucketType, "logs", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{"bucket": "logs", "tags": map[string]any{"team": "a"}}),
		}); err != nil {
			return err
		}
		_, err := monitor.RegisterResource(policyType, "pol", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{"bucket": "logs", "policy": "{}"}),
		})
		return err
	})
}

const bucketID = resource.ID("logs-id")

func policyURN() resource.URN {
	return resource.NewURN("test", "test", "", tokens.Type(policyType), "pol")
}

// otherStacksTags is the bucket's tags as stack "other" retagged them, the way choudoufu moves a resource between
// estates.
func otherStacksTags(t *testing.T) map[string]string {
	t.Helper()
	m, err := markers.For(resource.NewURN("other", "test", "", tokens.Type(bucketType), "logs"), markers.EncodingTags)
	require.NoError(t, err)
	m["team"] = "a"
	return m
}

// created runs the first `up`, with markers, and returns the store holding the bucket and the policy.
func created(t *testing.T, cloud *vetCloud, registered *bool) *countingStore {
	t.Helper()
	store := newCountingStore(t)
	*registered = true
	_, err := run(t, store, deploytest.NewPluginHostF(nil, nil, vetProgram(registered), nil, nil, cloud.loaders()...),
		runConfig{markers: true, persist: true})
	require.NoError(t, err)
	cloud.resetReads()
	return store
}

// errorText is every error diagnostic the run reported.
func errorText(events []engine.Event) string {
	var b strings.Builder
	for _, e := range events {
		if p, ok := e.Payload().(engine.DiagEventPayload); ok && p.Severity == diag.Error {
			b.WriteString(p.Message)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func hasRecord(t *testing.T, store *countingStore, urn resource.URN) bool {
	t.Helper()
	l, err := load(context.Background(), store, testKeys)
	require.NoError(t, err)
	k, ok := l.records[testKeys.resource(urn)]
	return ok && k.resource != nil
}

// The failure modes on the current code, without the vet: `up` without the bucket, and `destroy`, both delete an
// object whose live marker names another stack. This is the red the vetted tests below turn green.
func TestWithoutTheVetAnotherStacksObjectIsDeleted(t *testing.T) {
	t.Parallel()

	for _, destroy := range []bool{false, true} {
		cloud := newVetCloud()
		registered := true
		store := created(t, cloud, &registered)
		cloud.retag(bucketID, otherStacksTags(t))

		registered = false
		_, err := run(t, store, deploytest.NewPluginHostF(nil, nil, vetProgram(&registered), nil, nil,
			cloud.loaders()...), runConfig{markers: !destroy, persist: true, refresh: !destroy, destroy: destroy})
		require.NoError(t, err)
		assert.True(t, cloud.wasDeleted(bucketID), "destroy=%v: the record alone deleted stack other's bucket", destroy)
	}
}

// `up` without the bucket, and `destroy`, refuse the run when the bucket's live marker names another stack. The
// refusal names the URN, the stack the marker names and the record; nothing is deleted and the record survives.
func TestAnotherStacksMarkerRefusesUpAndDestroy(t *testing.T) {
	t.Parallel()

	for _, destroy := range []bool{false, true} {
		cloud := newVetCloud()
		registered := true
		store := created(t, cloud, &registered)
		cloud.retag(bucketID, otherStacksTags(t))

		registered = false
		events, err := run(t, store, deploytest.NewPluginHostF(nil, nil, vetProgram(&registered), nil, nil,
			cloud.loaders()...), runConfig{
			markers: !destroy, vet: true, persist: true, refresh: !destroy, destroy: destroy,
		})
		require.Error(t, err, "destroy=%v", destroy)
		msg := errorText(events)
		assert.Contains(t, msg, string(bucketURN()))
		assert.Contains(t, msg, `"test/other"`)
		assert.Contains(t, msg, testKeys.resource(bucketURN()))
		assert.Contains(t, msg, "plunge state delete")

		assert.False(t, cloud.wasDeleted(bucketID), "destroy=%v: the bucket survives", destroy)
		assert.False(t, cloud.wasDeleted("pol-id"), "destroy=%v: the refusal stops every delete", destroy)
		assert.True(t, hasRecord(t, store, bucketURN()), "destroy=%v: the record survives", destroy)
	}
}

// A taggable object whose markers were stripped is refused the same way: tags removed by hand are not consent.
func TestMissingMarkerRefusesUpAndDestroy(t *testing.T) {
	t.Parallel()

	for _, destroy := range []bool{false, true} {
		cloud := newVetCloud()
		registered := true
		store := created(t, cloud, &registered)
		cloud.retag(bucketID, map[string]string{"team": "a"})

		registered = false
		events, err := run(t, store, deploytest.NewPluginHostF(nil, nil, vetProgram(&registered), nil, nil,
			cloud.loaders()...), runConfig{
			markers: !destroy, vet: true, persist: true, refresh: !destroy, destroy: destroy,
		})
		require.Error(t, err, "destroy=%v", destroy)
		msg := errorText(events)
		assert.Contains(t, msg, string(bucketURN()))
		assert.Contains(t, msg, "carries no "+markers.TagStack+" marker")
		assert.Contains(t, msg, testKeys.resource(bucketURN()))

		assert.False(t, cloud.wasDeleted(bucketID), "destroy=%v", destroy)
		assert.True(t, hasRecord(t, store, bucketURN()), "destroy=%v", destroy)
	}
}

// The policy has no tags argument: it deletes on its record's word, its record's ownership entry says so, and the
// vet reads nothing for it. With refresh off (destroy), the bucket is read exactly once.
func TestRecordOwnedTypeStillDeletes(t *testing.T) {
	t.Parallel()

	cloud := newVetCloud()
	registered := true
	store := created(t, cloud, &registered)

	l, err := load(context.Background(), store, testKeys)
	require.NoError(t, err)
	rec := l.records[testKeys.resource(policyURN())].resource
	require.NotNil(t, rec.Ownership)
	assert.Equal(t, OwnershipRecord, rec.Ownership.By)
	assert.Equal(t, MarkerUntaggable, rec.Ownership.State)
	assert.Contains(t, rec.Ownership.Reason, "no tags argument")

	registered = false
	_, err = run(t, store, deploytest.NewPluginHostF(nil, nil, vetProgram(&registered), nil, nil, cloud.loaders()...),
		runConfig{vet: true, persist: true, destroy: true})
	require.NoError(t, err)
	assert.True(t, cloud.wasDeleted("pol-id"))
	assert.True(t, cloud.wasDeleted(bucketID), "the bucket carries this stack's markers")
	assert.Equal(t, 1, cloud.readCount(), "one read for the marked bucket, none for the record-owned policy")
	assert.False(t, hasRecord(t, store, policyURN()))
}

// With refresh on, the vet reads the markers off the refreshed outputs: the only provider reads are the refresh's
// own, one per resource.
func TestRefreshedVetMakesNoExtraRead(t *testing.T) {
	t.Parallel()

	cloud := newVetCloud()
	registered := true
	store := created(t, cloud, &registered)

	registered = false
	_, err := run(t, store, deploytest.NewPluginHostF(nil, nil, vetProgram(&registered), nil, nil, cloud.loaders()...),
		runConfig{markers: true, vet: true, persist: true, refresh: true})
	require.NoError(t, err)
	assert.True(t, cloud.wasDeleted(bucketID))
	assert.True(t, cloud.wasDeleted("pol-id"))
	assert.Equal(t, 2, cloud.readCount(), "the refresh reads the bucket and the policy once each; the vet adds none")
}
