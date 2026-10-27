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

// The acceptance test of INTENTIUS/plunge#1: one resource's drift is reported from one provider Read, with no
// stack-wide refresh and no snapshot assembly, as chant's drift record v1 stream.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/pkg/v3/secrets/b64"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

// spyStore counts every listing and bulk read that reaches the store. Assembling a snapshot needs one of them
// (readAll), so zero means no snapshot was assembled.
type spyStore struct {
	staterecord.Store
	mu      sync.Mutex
	lists   int
	getAlls int
}

func (s *spyStore) List(ctx context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	s.lists++
	s.mu.Unlock()
	return s.Store.List(ctx, prefix)
}

func (s *spyStore) GetAll(ctx context.Context, prefix string) (map[string]staterecord.Record, error) {
	s.mu.Lock()
	s.getAlls++
	s.mu.Unlock()
	return s.Store.(staterecord.BulkReader).GetAll(ctx, prefix)
}

const (
	driftProvURN = resource.URN("urn:pulumi:test::test::pulumi:providers:pkgA::default")
	driftProvID  = resource.ID("prov-1")
	driftResURN  = resource.URN("urn:pulumi:test::test::pkgA:m:typA::bucket")
	driftResID   = resource.ID("bucket-1")
)

// putRecord writes one resource record as the persister would.
func putRecord(t *testing.T, store staterecord.Store, state apitype.ResourceV3) {
	t.Helper()
	payload, err := json.Marshal(resourceRecord{Format: resourceFormat, URN: state.URN, States: []apitype.ResourceV3{state}})
	require.NoError(t, err)
	_, err = store.PutIfAbsent(context.Background(), testKeys.resource(state.URN), payload)
	require.NoError(t, err)
}

// seedDriftStack writes a provider record, a resource record whose outputs are outputs (serialized with the base64
// secrets manager), and the secrets record, plus an unrelated resource that the read must not touch.
func seedDriftStack(t *testing.T, store staterecord.Store, outputs resource.PropertyMap) {
	t.Helper()
	ctx := context.Background()
	sm := b64.NewBase64SecretsManager()

	putRecord(t, store, apitype.ResourceV3{
		URN: driftProvURN, Custom: true, ID: driftProvID, Type: "pulumi:providers:pkgA",
		Inputs: map[string]any{"version": "1.0.0"}, Outputs: map[string]any{"version": "1.0.0"},
	})
	serialized, err := stack.SerializeProperties(ctx, outputs, sm.Encrypter(), false)
	require.NoError(t, err)
	putRecord(t, store, apitype.ResourceV3{
		URN: driftResURN, Custom: true, ID: driftResID, Type: "pkgA:m:typA",
		Provider: string(driftProvURN) + "::" + string(driftProvID),
		Inputs:   map[string]any{}, Outputs: serialized,
	})
	putRecord(t, store, apitype.ResourceV3{
		URN: "urn:pulumi:test::test::pkgA:m:typA::other", Custom: true, ID: "other-1", Type: "pkgA:m:typA",
		Provider: string(driftProvURN) + "::" + string(driftProvID),
	})

	secretsPayload, err := json.Marshal(secretsRecord{Format: secretsFormat, Type: sm.Type(), State: sm.State()})
	require.NoError(t, err)
	_, err = store.PutIfAbsent(ctx, testKeys.secrets(), secretsPayload)
	require.NoError(t, err)
}

// driftPlugins is a plugin context whose only provider is pkgA, answering Read with read and counting the calls.
func driftPlugins(t *testing.T, read func(plugin.ReadRequest) plugin.ReadResponse) (*plugin.Context, *int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	loader := deploytest.NewProviderLoader("pkgA", semver.MustParse("1.0.0"), func() (plugin.Provider, error) {
		return &deploytest.Provider{
			ReadF: func(_ context.Context, req plugin.ReadRequest) (plugin.ReadResponse, error) {
				mu.Lock()
				calls++
				mu.Unlock()
				return read(req), nil
			},
		}, nil
	})
	host := deploytest.NewPluginHost(nil, nil, nil, loader)
	t.Cleanup(func() { contract.IgnoreClose(host) })
	pctx, err := plugin.NewContextWithHost(context.Background(), nil, nil, host, "", "", nil)
	require.NoError(t, err)
	t.Cleanup(func() { contract.IgnoreClose(pctx) })
	return pctx, &calls
}

func driftRequest(pctx *plugin.Context) DriftRequest {
	return DriftRequest{
		URN:             driftResURN,
		SecretsProvider: b64.Base64SecretsProvider,
		Plugins:         pctx,
		Member:          "test",
		Version:         "v0.0.0-test",
		Command:         "plunge drift --resource " + string(driftResURN) + " --stream",
		Now:             func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) },
	}
}

func TestOneResourceDriftFromOneRead(t *testing.T) {
	t.Parallel()
	local, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	store := &spyStore{Store: local}

	seedDriftStack(t, local, resource.PropertyMap{
		"name":     resource.NewProperty("logs"),
		"tags":     resource.NewProperty(resource.PropertyMap{"env": resource.NewProperty("dev")}),
		"password": resource.MakeSecret(resource.NewProperty("hunter2")),
	})

	pctx, calls := driftPlugins(t, func(req plugin.ReadRequest) plugin.ReadResponse {
		live := property.NewMap(map[string]property.Value{
			"name":     property.New("logs"),
			"tags":     property.New(property.NewMap(map[string]property.Value{"env": property.New("prod")})),
			"password": property.New("changed").WithSecret(true),
		})
		return plugin.ReadResponse{ReadResult: plugin.ReadResult{ID: req.ID, Outputs: &live}}
	})

	res, err := resourceDrift(context.Background(), store, testKeys, driftRequest(pctx))
	require.NoError(t, err)

	// One provider Read, no listing or bulk read of the store (so no snapshot assembly), no stack refresh.
	assert.Equal(t, 1, *calls)
	assert.Equal(t, 0, store.lists)
	assert.Equal(t, 0, store.getAlls)
	assert.Equal(t, DriftReads{
		RecordsRead: 3, RecordKeys: 3, RecordListCalls: 0, ProviderReads: 1, StackRefreshes: 0,
	}, res.Reads)

	r := res.Record
	assert.Equal(t, "drifted", r.Status)
	assert.Equal(t, "plunge", r.Engine)
	assert.Equal(t, "refresh", r.Read)
	assert.Equal(t, string(driftResURN), r.Address)
	assert.Nil(t, r.Node)
	assert.Equal(t, "2026-10-25T12:00:00Z", r.ObservedAt)
	assert.Equal(t, []DriftAttribute{
		{Path: "password", Sensitive: true},
		{Path: "tags.env", Before: "dev", After: "prod"},
	}, r.Attributes)
	assert.Equal(t, DriftEvidence{Tool: "plunge", Version: "v0.0.0-test", Command: driftRequest(nil).Command}, r.Evidence)

	// The stream: three lines, each with a type, in the shape chant's decodeDriftLine reads.
	var buf bytes.Buffer
	require.NoError(t, WriteDriftStream(&buf, res))
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 3)

	var start, record, settle map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &start))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &record))
	require.NoError(t, json.Unmarshal([]byte(lines[2]), &settle))

	assert.Equal(t, "drift-start", start["type"])
	assert.Equal(t, "v1", start["drift"])
	assert.Equal(t, []any{string(driftResURN)}, start["expected"])

	assert.Equal(t, "drift-record", record["type"])
	assert.Contains(t, record, "node")
	assert.Nil(t, record["node"])
	assert.NotContains(t, lines[1], "hunter2")
	assert.NotContains(t, lines[1], "changed")

	assert.Equal(t, "drift-settle", settle["type"])
	assert.Equal(t, map[string]any{"drifted": float64(1)}, settle["counts"])
	assert.Equal(t, map[string]any{
		"recordsRead": float64(3), "recordKeys": float64(3), "recordListCalls": float64(0),
		"providerReads": float64(1), "stackRefreshes": float64(0),
	}, settle["reads"])
}

func TestResourceDriftInSyncReadsNoSecretsRecord(t *testing.T) {
	t.Parallel()
	local, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	store := &spyStore{Store: local}
	seedDriftStack(t, local, resource.PropertyMap{"name": resource.NewProperty("logs")})

	pctx, calls := driftPlugins(t, func(req plugin.ReadRequest) plugin.ReadResponse {
		return plugin.ReadResponse{ReadResult: plugin.ReadResult{ID: req.ID, Outputs: &req.State}}
	})

	res, err := resourceDrift(context.Background(), store, testKeys, driftRequest(pctx))
	require.NoError(t, err)
	assert.Equal(t, "in-sync", res.Record.Status)
	assert.Empty(t, res.Record.Attributes)
	assert.Equal(t, 1, *calls)
	// The resource's record and its provider's; no secret, so the secrets record is not read.
	assert.Equal(t, 2, res.Reads.RecordsRead)
	assert.Equal(t, 0, store.lists+store.getAlls)
}

func TestResourceDriftMissing(t *testing.T) {
	t.Parallel()
	local, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	seedDriftStack(t, local, resource.PropertyMap{"name": resource.NewProperty("logs")})

	pctx, calls := driftPlugins(t, func(plugin.ReadRequest) plugin.ReadResponse {
		return plugin.ReadResponse{}
	})

	res, err := resourceDrift(context.Background(), local, testKeys, driftRequest(pctx))
	require.NoError(t, err)
	assert.Equal(t, "missing", res.Record.Status)
	assert.Equal(t, 1, *calls)
}

func TestResourceDriftNoRecordIsRefusedByName(t *testing.T) {
	t.Parallel()
	local, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)

	_, err = resourceDrift(context.Background(), local, testKeys, driftRequest(nil))
	var noRecord *NoRecordError
	require.ErrorAs(t, err, &noRecord)
	assert.Equal(t, driftResURN, noRecord.URN)
}
