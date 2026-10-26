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

// The acceptance tests of INTENTIUS/plunge#14: resource counts in `stack ls`, history and backups refused by name,
// a dependent of another run's in-flight resource refused, clearing a pending operation by import, the Azure Blob
// account-settings contract on open, and the drift read's old views, status service and builtin provider.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

// newFileBackend opens the plunge backend on plunge+file:// over a temp directory, with the "test" stack of the
// "test" project created, so its records are under testKeys.
func newFileBackend(t *testing.T) (*plungeBackend, backend.Stack) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	sink := diag.DefaultSink(io.Discard, io.Discard, diag.FormatOptions{Color: colors.Never})
	be, err := New(ctx, sink, URLPrefix+"file://"+filepath.ToSlash(dir), nil)
	require.NoError(t, err)
	pb := be.(*plungeBackend)
	ref, err := pb.ParseStackReference("organization/test/test")
	require.NoError(t, err)
	s, err := pb.CreateStack(ctx, ref, "", nil, nil)
	require.NoError(t, err)
	return pb, s
}

func TestStackListCountsResourcesFromRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pb, _ := newFileBackend(t)

	l, err := load(ctx, pb.store, testKeys)
	require.NoError(t, err)
	p := newRecordPersister(ctx, pb.store, l, Generation{}, true, nil)
	require.NoError(t, p.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{Resources: []apitype.ResourceV3{
		{URN: "urn:pulumi:test::test::pkgA:m:typA::a", Type: "pkgA:m:typA", Custom: true, ID: "a-1"},
		{URN: "urn:pulumi:test::test::pkgA:m:typA::b", Type: "pkgA:m:typA", Custom: true, ID: "b-1"},
	}}}))

	summaries, _, err := pb.ListStacks(ctx, backend.ListStacksFilter{}, nil)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	count := summaries[0].ResourceCount()
	require.NotNil(t, count)
	assert.Equal(t, 2, *count)
}

func TestStackHistoryAndBackupsAreRefusedByName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pb, s := newFileBackend(t)

	var unsupported *UnsupportedOnRecordsError

	_, err := pb.GetHistory(ctx, s.Ref(), 10, 1)
	require.ErrorAs(t, err, &unsupported)
	assert.Equal(t, "stack history", unsupported.What)
	assert.Contains(t, err.Error(), "keeps no stack history")

	_, err = pb.ExportDeploymentForVersion(ctx, s, "1")
	require.ErrorAs(t, err, &unsupported)
	assert.Equal(t, "previous deployment versions", unsupported.What)

	_, err = pb.RemoveStack(ctx, s, false, true /* removeBackups */)
	require.ErrorAs(t, err, &unsupported)
	assert.Equal(t, "checkpoint backups", unsupported.What)

	// The stack is still there: the refusal removed nothing.
	got, err := pb.GetStack(ctx, s.Ref())
	require.NoError(t, err)
	assert.NotNil(t, got)
}

// programWithDependent registers resX, then resD with resX as a dependency and the given "v" input.
func programWithDependent(d string) deploytest.LanguageRuntimeFactory {
	return deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		x, err := monitor.RegisterResource("pkgA:m:typA", "resX", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{"v": "1"}),
		})
		if err != nil {
			return err
		}
		_, err = monitor.RegisterResource("pkgA:m:typA", "resD", true, deploytest.ResourceOptions{
			Inputs:       resource.NewPropertyMapFromMap(map[string]any{"v": d}),
			Dependencies: []resource.URN{x.URN},
		})
		return err
	})
}

// startInFlightCreate writes, into resX's record, the pending create run A has in flight on it (a create-before-
// delete replacement: the old state stays, the new one is pending), with the outputs A's unfinished create holds.
func startInFlightCreate(t *testing.T, store staterecord.Store, urnX resource.URN) {
	t.Helper()
	ctx := context.Background()
	key := testKeys.resource(urnX)
	payload, version, exists, err := store.Get(ctx, key)
	require.NoError(t, err)
	require.True(t, exists)
	var r resourceRecord
	require.NoError(t, json.Unmarshal(payload, &r))
	require.NotEmpty(t, r.States)
	inflight := r.States[0]
	inflight.ID = ""
	inflight.Outputs = map[string]any{"v": "A-inflight"}
	r.Pending = append(r.Pending, apitype.OperationV2{Resource: inflight, Type: apitype.OperationTypeCreating})
	updated, err := json.Marshal(r)
	require.NoError(t, err)
	_, err = store.PutIfVersion(ctx, key, updated, version)
	require.NoError(t, err)
}

func TestDependentOfAnotherRunsInFlightResourceIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newCountingStore(t)
	gen := Generation{Commit: "c1", Time: 100}
	urnX := resource.URN("urn:pulumi:test::test::pkgA:m:typA::resX")
	urnD := resource.URN("urn:pulumi:test::test::pkgA:m:typA::resD")

	require.NoError(t, runUpdate(t, store,
		deploytest.NewPluginHostF(nil, nil, programWithDependent("1"), nil, nil, testLoaders(nil)...), gen))
	before, _, _, err := store.Get(ctx, testKeys.resource(urnD))
	require.NoError(t, err)

	// Run A has a create in flight on resX. Run B changes resD, which depends on resX.
	startInFlightCreate(t, store, urnX)
	err = runUpdate(t, store,
		deploytest.NewPluginHostF(nil, nil, programWithDependent("2"), nil, nil, testLoaders(nil)...), gen)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "in flight")
	assert.Contains(t, err.Error(), string(urnD))
	assert.Contains(t, err.Error(), string(urnX))

	// resD's record is as the base run left it: it holds no value from A's in-flight state, and B wrote nothing.
	after, _, _, err := store.Get(ctx, testKeys.resource(urnD))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
	assert.NotContains(t, string(after), "A-inflight")
}

// The same refusal at the persister, where the error is typed: the save that begins B's operation on resD is the one
// refused, so the provider is never called for it.
func TestBeginningAnOperationOnADependentOfAnInFlightResourceIsRefusedByName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newCountingStore(t)
	urnX := resource.URN("urn:pulumi:test::test::pkgA:m:typA::resX")
	urnD := resource.URN("urn:pulumi:test::test::pkgA:m:typA::resD")
	x := apitype.ResourceV3{URN: urnX, Type: "pkgA:m:typA", Custom: true, ID: "x-1", Outputs: map[string]any{"v": "1"}}
	d := apitype.ResourceV3{
		URN: urnD, Type: "pkgA:m:typA", Custom: true, ID: "d-1", Dependencies: []resource.URN{urnX},
		Inputs: map[string]any{"v": "1"},
	}

	l, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	base := newRecordPersister(ctx, store, l, Generation{}, true, nil)
	require.NoError(t, base.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
		Resources: []apitype.ResourceV3{x, d},
	}}))

	// B reads the records, then A begins a create on resX.
	l, err = load(ctx, store, testKeys)
	require.NoError(t, err)
	startInFlightCreate(t, store, urnX)

	b := newRecordPersister(ctx, store, l, Generation{}, true, nil)
	d2 := d
	d2.Inputs = map[string]any{"v": "2"}
	err = b.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
		Resources:         []apitype.ResourceV3{x, d},
		PendingOperations: []apitype.OperationV2{{Resource: d2, Type: apitype.OperationTypeUpdating}},
	}})
	var inflight *InFlightDependencyError
	require.ErrorAs(t, err, &inflight)
	assert.Equal(t, urnD, inflight.URN)
	assert.Equal(t, urnX, inflight.Dependency)
	assert.Equal(t, apitype.OperationTypeCreating, inflight.Operation)
	assert.Equal(t, testKeys.resource(urnX), inflight.Key)

	// Nothing was written for resD.
	l, err = load(ctx, store, testKeys)
	require.NoError(t, err)
	for _, op := range l.deployment.Deployment.PendingOperations {
		assert.NotEqual(t, urnD, op.Resource.URN)
	}
}

// A crashed run's pending operation stays in its record until a run acts on the URN. Importing a deployment without
// it clears it: the import is authoritative about pending operations.
func TestImportClearsACrashedRunsPendingOperation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newCountingStore(t)
	urnX := resource.URN("urn:pulumi:test::test::pkgA:m:typA::resX")
	x := apitype.ResourceV3{URN: urnX, Type: "pkgA:m:typA", Custom: true, ID: "x-1"}

	l, err := load(ctx, store, testKeys)
	require.NoError(t, err)
	crashed := newRecordPersister(ctx, store, l, Generation{}, true, nil)
	require.NoError(t, crashed.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
		Resources:         []apitype.ResourceV3{x},
		PendingOperations: []apitype.OperationV2{{Resource: x, Type: apitype.OperationTypeUpdating}},
	}}))

	// A run that does not act on resX keeps the pending operation.
	l, err = load(ctx, store, testKeys)
	require.NoError(t, err)
	other := newRecordPersister(ctx, store, l, Generation{}, true, nil)
	require.NoError(t, other.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
		Resources: []apitype.ResourceV3{x},
	}}))
	l, err = load(ctx, store, testKeys)
	require.NoError(t, err)
	require.Len(t, l.deployment.Deployment.PendingOperations, 1)

	// An import of the export with the pending operation removed clears it.
	imp := newRecordPersister(ctx, store, l, Generation{}, false, nil)
	imp.authoritative = true
	require.NoError(t, imp.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
		Resources: []apitype.ResourceV3{x},
	}}))
	l, err = load(ctx, store, testKeys)
	require.NoError(t, err)
	assert.Empty(t, l.deployment.Deployment.PendingOperations)
}

// fakeAzureAccount is an AzureContractAPI for an account with versioning off, no lifecycle policy, and anonymous
// access allowed: none of the settings the contract requires.
type fakeAzureAccount struct{}

func (fakeAzureAccount) BlobServiceProperties(context.Context) (armstorage.BlobServiceProperties, error) {
	off := false
	return armstorage.BlobServiceProperties{
		BlobServiceProperties: &armstorage.BlobServicePropertiesProperties{IsVersioningEnabled: &off},
	}, nil
}

func (fakeAzureAccount) ManagementPolicy(context.Context) (armstorage.ManagementPolicy, error) {
	return armstorage.ManagementPolicy{}, nil
}

func (fakeAzureAccount) Account(context.Context) (armstorage.Account, error) {
	on := true
	return armstorage.Account{Properties: &armstorage.AccountProperties{AllowBlobPublicAccess: &on}}, nil
}

func (fakeAzureAccount) Container(context.Context) (armstorage.BlobContainer, error) {
	return armstorage.BlobContainer{}, nil
}

func fakeAzureStore(t *testing.T) *staterecord.AzureBlobStore {
	t.Helper()
	client, err := container.NewClientWithNoCredential("https://plungetest.blob.core.windows.net/records", nil)
	require.NoError(t, err)
	store, err := staterecord.NewAzureBlobStore(staterecord.AzureBlobConfig{
		Container:     client,
		Account:       "plungetest",
		ContainerName: "records",
		ResourceGroup: "rg",
		KeyPrefix:     recordsDir,
		Contract:      fakeAzureAccount{},
	})
	require.NoError(t, err)
	return store
}

func TestAzureBlobStoreLackingRequiredSettingsIsRefusedByName(t *testing.T) {
	t.Parallel()
	err := enforceContract(context.Background(), fakeAzureStore(t), nil)
	var refused *ContractRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Settings, staterecord.AzureBlobVersioning)
	assert.Contains(t, refused.Settings, staterecord.AzurePublicAccess)
	assert.Contains(t, err.Error(), "blob_versioning")
	assert.Contains(t, err.Error(), "plungetest")
	assert.Contains(t, err.Error(), "allow_insecure")
}

func TestAzureBlobStoreWaivedSettingsOpen(t *testing.T) {
	t.Parallel()
	waived := []string{
		string(staterecord.AzureBlobVersioning), string(staterecord.AzureLifecycle), string(staterecord.AzurePublicAccess),
	}
	require.NoError(t, enforceContract(context.Background(), fakeAzureStore(t), waived))
}

// An Azure store with no resource group or subscription cannot read its settings, and is refused naming each.
func TestAzureBlobStoreWithoutContractIsRefused(t *testing.T) {
	t.Parallel()
	client, err := container.NewClientWithNoCredential("https://plungetest.blob.core.windows.net/records", nil)
	require.NoError(t, err)
	store, err := staterecord.NewAzureBlobStore(staterecord.AzureBlobConfig{
		Container: client, Account: "plungetest", ContainerName: "records",
		ContractUnavailable: "set the resource_group and subscription_id query keys",
	})
	require.NoError(t, err)
	err = enforceContract(context.Background(), store, nil)
	var refused *ContractRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Len(t, refused.Settings, len(staterecord.AzureSettings))
	assert.Contains(t, err.Error(), "resource_group")
}

func TestKubernetesURLNeedsAMetadataURL(t *testing.T) {
	t.Parallel()
	assert.True(t, IsPlungeBackendURL("plunge+k8s://records"))
	_, err := metadataURL("plunge+k8s://records")
	var missing *MissingMetadataURLError
	require.ErrorAs(t, err, &missing)

	meta, err := metadataURL("plunge+k8s://records/team-a?metadata=file:///shared/meta&context=kind-plunge")
	require.NoError(t, err)
	assert.Equal(t, "file:///shared/meta", meta)
}

// A provider that reads its view resources during Read gets them: the record lists the views, the drift read reads
// each by key and passes them as OldViews, with a resource status address and token to publish view steps to.
func TestResourceDriftPassesOldViewsAndResourceStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	local, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	store := &spyStore{Store: local}
	seedDriftStack(t, local, resource.PropertyMap{"name": resource.NewProperty("logs")})

	// The view's record, and the viewed resource's record naming it.
	viewURN := resource.URN("urn:pulumi:test::test::pkgA:m:typA$pkgA:m:view::bucket-policy")
	putRecord(t, local, apitype.ResourceV3{
		URN: viewURN, Type: "pkgA:m:view", Custom: true, ID: "view-1", ViewOf: driftResURN, Parent: driftResURN,
		Inputs: map[string]any{"rule": "deny"}, Outputs: map[string]any{"rule": "deny"},
	})
	key := testKeys.resource(driftResURN)
	payload, version, _, err := local.Get(ctx, key)
	require.NoError(t, err)
	var r resourceRecord
	require.NoError(t, json.Unmarshal(payload, &r))
	r.Views = []resource.URN{viewURN}
	updated, err := json.Marshal(r)
	require.NoError(t, err)
	_, err = local.PutIfVersion(ctx, key, updated, version)
	require.NoError(t, err)

	var mu sync.Mutex
	var got plugin.ReadRequest
	pctx, calls := driftPlugins(t, func(req plugin.ReadRequest) plugin.ReadResponse {
		mu.Lock()
		got = req
		mu.Unlock()
		return plugin.ReadResponse{ReadResult: plugin.ReadResult{ID: req.ID, Outputs: &req.State}}
	})

	res, err := resourceDrift(ctx, store, testKeys, driftRequest(pctx))
	require.NoError(t, err)
	assert.Equal(t, 1, *calls)
	assert.Equal(t, "in-sync", res.Record.Status)
	assert.Equal(t, 0, store.lists+store.getAlls, "views are read by key, not by a listing")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got.OldViews, 1)
	assert.Equal(t, "bucket-policy", got.OldViews[0].Name)
	assert.Equal(t, "pkgA:m:view", string(got.OldViews[0].Type))
	assert.Equal(t, "deny", got.OldViews[0].Outputs.Get("rule").AsString())
	assert.NotEmpty(t, got.ResourceStatusAddress)
	assert.NotEmpty(t, got.ResourceStatusToken)
}

// fakeStacks is a deploy.BackendClient holding one other stack's outputs.
type fakeStacks map[string]property.Map

func (f fakeStacks) GetStackOutputs(_ context.Context, name string, _ func(error) error) (property.Map, error) {
	if out, ok := f[name]; ok {
		return out, nil
	}
	return property.Map{}, errors.New("no such stack " + name)
}

func (f fakeStacks) GetStackResourceOutputs(context.Context, string) (property.Map, error) {
	return property.Map{}, nil
}

// A StackReference is a resource of the builtin `pulumi` provider. Its drift read loads that provider, reads the
// other stack's outputs through the backend client, and gives a drift record.
func TestResourceDriftOfAStackReference(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	local, err := staterecord.NewLocalStore(t.TempDir())
	require.NoError(t, err)

	provURN := resource.URN("urn:pulumi:test::test::pulumi:providers:pulumi::default")
	refURN := resource.URN("urn:pulumi:test::test::pulumi:pulumi:StackReference::other")
	putRecord(t, local, apitype.ResourceV3{
		URN: provURN, Custom: true, ID: "prov-p", Type: "pulumi:providers:pulumi",
	})
	putRecord(t, local, apitype.ResourceV3{
		URN: refURN, Custom: true, ID: "organization/test/other", Type: "pulumi:pulumi:StackReference",
		Provider: string(provURN) + "::prov-p",
		Inputs:   map[string]any{"name": "organization/test/other"},
		Outputs: map[string]any{
			"name":              "organization/test/other",
			"outputs":           map[string]any{"endpoint": "https://old.example"},
			"secretOutputNames": []any{},
		},
	})

	pctx, calls := driftPlugins(t, func(plugin.ReadRequest) plugin.ReadResponse {
		t.Fatal("the pkgA provider must not be called for a StackReference")
		return plugin.ReadResponse{}
	})
	req := driftRequest(pctx)
	req.URN = refURN
	req.BackendClient = fakeStacks{
		"organization/test/other": property.NewMap(map[string]property.Value{"endpoint": property.New("https://new.example")}),
	}

	res, err := resourceDrift(ctx, local, testKeys, req)
	require.NoError(t, err)
	assert.Equal(t, 0, *calls)
	assert.Equal(t, "drifted", res.Record.Status, "the record: %+v", res.Record)
	assert.Equal(t, "pulumi:pulumi:StackReference", res.Record.ResourceType)
	assert.Equal(t, []DriftAttribute{
		{Path: "outputs.endpoint", Before: "https://old.example", After: "https://new.example"},
	}, res.Record.Attributes)
}
