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

// The acceptance tests of INTENTIUS/plunge#52, `plunge stack adopt --adopt`, against the local record store with
// plunge#51's liveCloud provider standing in for pulumi-aws and an in-memory map standing in for the cloud. No
// emulator and no cloud: the refusal tests hold objects another stack owns, so they run on fakes only. STS and Cloud
// Control are replaced by the adoption's test hooks, so nothing leaves the process.
//
//   - the gate: unconfirmed, the adoption reads each object once and writes nothing; confirmed, it writes the
//     records and the adoption record, and no marker (the cloud sees no update); the view lists each marker write
//     as tags only; adopting the same file again writes nothing;
//   - refusals, each before anything is written: a create-only-tags type (before any provider call), another
//     stack's live marker, this stack's marker for another resource, an object the read does not find, a selection
//     with no Pulumi type, an `as` that is not a name, a provider configuration that hides the markers, a stack
//     whose stock checkpoint holds resources;
//   - an object already carrying its markers is adopted with no marker write; a record-owned type is adopted on
//     its record;
//   - StackProviders configures the default provider from the stack's configuration, named as the engine names it.
//
// Written for plunge#52 and not run in that unit.

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/intentius/choudoufu/pkg/adoption"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	pkgresource "github.com/pulumi/pulumi/pkg/v3/resource"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/pkg/v3/secrets/b64"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

const testDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeProviders hands out the liveCloud's provider as the stack's default aws provider.
type fakeProviders struct {
	cloud  *liveCloud
	inputs map[string]any
	calls  int
}

func (f *fakeProviders) Provider(_ context.Context, pkg tokens.Package) (plugin.Provider, *pkgresource.State, error) {
	f.calls++
	inputs := map[string]any{"version": "7.0.0", "region": "us-east-1"}
	for k, v := range f.inputs {
		inputs[k] = v
	}
	urn := resource.NewURN("test", "test", "", "pulumi:providers:"+tokens.Type(pkg), "default_7_0_0")
	return f.cloud.provider(), pkgresource.NewState{
		Type: urn.Type(), URN: urn, Custom: true, ID: "prov-1",
		Inputs:  resource.NewPropertyMapFromMap(inputs),
		Outputs: resource.NewPropertyMapFromMap(inputs),
	}.Make(), nil
}

// sel is one selection of an adoption file: an aws node by its listed type and native ID.
type sel struct {
	listed, native, as, typ string
	substrate               string
}

func testAdoption(sels ...sel) *adoption.Adoption {
	f := &adoption.File{Adopt: adoption.FormatVersion, Index: adoption.IndexRef{
		ScopeKey: "cloud/aws/123456789012/abcdef012345",
		Version:  "20261026T120000Z-" + testDigest[:12] + ".json",
		Digest:   "sha256:" + testDigest,
	}}
	a := &adoption.Adoption{File: f, Index: &adoption.IndexVersion{Stamp: adoption.Stamp{
		ScopeKey: f.Index.ScopeKey, Digest: testDigest, ObservedAt: "2026-10-26T12:00:00Z",
	}}}
	for _, s := range sels {
		substrate := s.substrate
		if substrate == "" {
			substrate = "aws"
		}
		node := &adoption.Node{
			ID: substrate + "/123456789012/us-east-1/" + s.listed + "/" + s.native, Kind: "resource",
			ListedType: s.listed, Location: "us-east-1", NativeID: s.native,
		}
		selection := adoption.Selection{Node: node.ID, As: s.as, Type: s.typ}
		f.Select = append(f.Select, selection)
		a.Entries = append(a.Entries, adoption.Entry{Selection: selection, Node: node})
	}
	return a
}

func bucketSel() sel {
	return sel{listed: "AWS::S3::Bucket", native: string(bucketID), as: "logs", typ: bucketType}
}

// indexOpts is an index adoption over cloud, with STS and Cloud Control stood in for.
func indexOpts(cloud *liveCloud, a *adoption.Adoption, confirmed bool) (AdoptOptions, *fakeProviders) {
	provs := &fakeProviders{cloud: cloud}
	return AdoptOptions{
		Confirmed:       confirmed,
		SecretsProvider: stack.Base64SecretsProvider{},
		Index: &IndexAdoptOptions{
			Adoption:       a,
			File:           "adopt.json",
			Providers:      provs,
			SecretsManager: b64.NewBase64SecretsManager(),
			accounts: &accountIDs{call: func(context.Context, string, string, aws.CredentialsProvider) (string, error) {
				return "123456789012", nil
			}},
			readModel: func(_ context.Context, _ TypeInfo, _ resource.PropertyMap, id string) (map[string]any, error) {
				return map[string]any{"BucketName": id, "Arn": "arn:aws:s3:::" + id}, nil
			},
		},
	}, provs
}

func refusalCodes(t *testing.T, err error) []AdoptRefusalCode {
	t.Helper()
	var refused *AdoptRefusedError
	require.ErrorAs(t, err, &refused)
	var out []AdoptRefusalCode
	for _, r := range refused.Refusals {
		out = append(out, r.Code)
	}
	return out
}

// The gate: unconfirmed, one live read and no write; confirmed, the records and no marker; again, nothing.
func TestIndexAdoptionIsGatedAndWritesNoMarker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cloud := newLiveCloud()
	cloud.put(bucketID, map[string]any{"bucket": "logs", "tags": map[string]any{"team": "a"}})
	f := newAdoptFixture(t, nil)
	a := testAdoption(bucketSel())

	opts, _ := indexOpts(cloud, a, false)
	_, err := f.backend.Adopt(ctx, f.stack, opts)
	var unconfirmed *AdoptUnconfirmedError
	require.ErrorAs(t, err, &unconfirmed)
	assert.Zero(t, f.store.total(), "unconfirmed, nothing is written")
	assert.Equal(t, 1, cloud.readCount(), "each object is read live once")
	require.Len(t, unconfirmed.Result.Resources, 1)
	r := unconfirmed.Result.Resources[0]
	assert.Equal(t, bucketURN(), r.URN)
	assert.Equal(t, bucketID, r.ID)
	assert.Equal(t, bucketID, r.ImportID)
	assert.Equal(t, OwnershipMarker, r.Ownership)
	assert.False(t, r.Marked)
	want, err := markers.For(bucketURN(), markers.EncodingTags)
	require.NoError(t, err)
	require.NotNil(t, r.MarkerWrite, "the marker write the first up makes is listed")
	assert.Equal(t, "tags", r.MarkerWrite.Property)
	assert.Equal(t, want, r.MarkerWrite.Tags)

	// The view lists it as tags only, with the index version.
	view, err := NewAdoptView("organization/test/test", nil, err)
	require.NoError(t, err)
	assert.Equal(t, AdoptWouldAdopt, view.Status)
	require.NotNil(t, view.Index)
	assert.Equal(t, a.File.Index.Version, view.Index.Version)
	require.NotNil(t, view.Resources[0].MarkerWrite)
	assert.True(t, view.Resources[0].MarkerWrite.TagsOnly)
	assert.Equal(t, want, view.Resources[0].MarkerWrite.Tags)

	opts, _ = indexOpts(cloud, a, true)
	res, err := f.backend.Adopt(ctx, f.stack, opts)
	require.NoError(t, err)
	assert.True(t, res.Written)
	assert.Zero(t, cloud.updateCount(bucketID), "adoption writes no marker; the first up does")
	assert.Empty(t, cloud.tags(bucketID)[markers.TagStack])

	snap := assembled(t, f.store)
	var bucket *pkgresource.State
	for _, s := range snap.Resources {
		if s.URN == bucketURN() {
			bucket = s
		}
	}
	require.NotNil(t, bucket, "the bucket has a record")
	assert.Equal(t, bucketID, bucket.ID)
	assert.Equal(t, "logs", bucket.Outputs["bucket"].StringValue())
	assert.NotEmpty(t, bucket.Provider, "the record names the stack's default provider")
	assert.Equal(t, resource.RootStackType, bucket.Parent.Type())

	l, err := load(ctx, f.store, testKeys)
	require.NoError(t, err)
	require.NotNil(t, l.adoption)
	require.NotNil(t, l.adoption.Index)
	assert.Equal(t, a.File.Index.ScopeKey, l.adoption.Index.ScopeKey)
	assert.Equal(t, bucketSel().as, l.adoption.Resources[0].URN.Name())
	assert.Equal(t, a.Entries[0].Node.ID, l.adoption.Resources[0].Node)
	e, ok := l.records[testKeys.resource(bucketURN())]
	require.True(t, ok)
	raw, err := json.Marshal(e.resource)
	require.NoError(t, err)
	assert.Contains(t, string(raw), string(MarkerAdoptedPending), "the record says its markers are pending")

	before := f.store.total()
	opts, _ = indexOpts(cloud, a, true)
	again, err := f.backend.Adopt(ctx, f.stack, opts)
	require.NoError(t, err)
	assert.True(t, again.AlreadyAdopted)
	assert.Equal(t, before, f.store.total(), "adopting the same file again writes nothing")
}

// A create-only-tags type is refused by name before any provider call; nothing is written.
func TestIndexAdoptionRefusesCreateOnlyTagsBeforeAnyRead(t *testing.T) {
	t.Parallel()

	cloud := newLiveCloud()
	cloud.put("t-1", map[string]any{"tags": map[string]any{"team": "a"}})
	f := newAdoptFixture(t, nil)
	opts, _ := indexOpts(cloud, testAdoption(sel{listed: "AWS::M::Thing", native: "t-1", as: "t", typ: thingType}), true)
	_, err := f.backend.Adopt(context.Background(), f.stack, opts)
	assert.Equal(t, []AdoptRefusalCode{RefuseCreateOnlyTags}, refusalCodes(t, err))
	assert.Contains(t, err.Error(), string(thingURN("t")))
	assert.Contains(t, err.Error(), "never replaces")
	assert.Zero(t, cloud.readCount(), "the type table decides; no provider call")
	assert.Zero(t, f.store.total())
}

// Another stack's live marker, and this stack's marker for another resource, refuse the object; nothing is written
// and the other stack's marker is left as it was.
func TestIndexAdoptionRefusesAnotherOwnersMarker(t *testing.T) {
	t.Parallel()

	for _, owner := range []struct{ stack, name string }{{"other", "logs"}, {"test", "elsewhere"}} {
		cloud := newLiveCloud()
		cloud.put(bucketID, map[string]any{"bucket": "logs"})
		theirs := stackTags(t, owner.stack, bucketType, owner.name)
		cloud.retag(bucketID, theirs)
		f := newAdoptFixture(t, nil)
		opts, _ := indexOpts(cloud, testAdoption(bucketSel()), true)
		_, err := f.backend.Adopt(context.Background(), f.stack, opts)
		assert.Equal(t, []AdoptRefusalCode{RefuseForeignMarker}, refusalCodes(t, err), "owner %v", owner)
		assert.Contains(t, err.Error(), string(bucketURN()))
		assert.Zero(t, f.store.total(), "owner %v: nothing is written", owner)
		assert.Equal(t, theirs, cloud.tags(bucketID))
		assert.Zero(t, cloud.updateCount(bucketID))
	}
}

// An object already carrying this resource's markers is adopted with no marker write.
func TestIndexAdoptionOfAMarkedObjectWritesNoMarker(t *testing.T) {
	t.Parallel()

	cloud := newLiveCloud()
	cloud.put(bucketID, map[string]any{"bucket": "logs"})
	cloud.retag(bucketID, stackTags(t, "test", bucketType, "logs"))
	f := newAdoptFixture(t, nil)
	opts, _ := indexOpts(cloud, testAdoption(bucketSel()), true)
	res, err := f.backend.Adopt(context.Background(), f.stack, opts)
	require.NoError(t, err)
	require.Len(t, res.Resources, 1)
	assert.True(t, res.Resources[0].Marked)
	assert.Nil(t, res.Resources[0].MarkerWrite)

	view, err := NewAdoptView("organization/test/test", res, nil)
	require.NoError(t, err)
	assert.Nil(t, view.Resources[0].MarkerWrite)
	assert.True(t, view.Resources[0].Marked)
}

// A record-owned type is adopted on its record alone, with no marker to write.
func TestIndexAdoptionOfARecordOwnedType(t *testing.T) {
	t.Parallel()

	cloud := newLiveCloud()
	cloud.put("logs", map[string]any{"bucket": "logs", "policy": "{}"})
	f := newAdoptFixture(t, nil)
	opts, _ := indexOpts(cloud, testAdoption(sel{
		listed: "AWS::S3::BucketPolicy", native: "logs", as: "pol", typ: "aws:s3/bucketPolicy:BucketPolicy",
	}), false)
	_, err := f.backend.Adopt(context.Background(), f.stack, opts)
	var unconfirmed *AdoptUnconfirmedError
	require.ErrorAs(t, err, &unconfirmed)
	require.Len(t, unconfirmed.Result.Resources, 1)
	assert.Equal(t, OwnershipRecord, unconfirmed.Result.Resources[0].Ownership)
	assert.Nil(t, unconfirmed.Result.Resources[0].MarkerWrite)
}

// The rest of the refusals, each by code and before anything is written.
func TestIndexAdoptionRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		sel    sel
		inputs map[string]any
		want   AdoptRefusalCode
	}{
		{name: "not found", sel: sel{listed: "AWS::S3::Bucket", native: "gone", as: "logs", typ: bucketType},
			want: RefuseNotFound},
		{name: "no type", sel: sel{substrate: "azure", listed: "microsoft.storage/storageaccounts",
			native: "/subscriptions/s/resourceGroups/g/providers/Microsoft.Storage/storageAccounts/a", as: "acct"},
			want: RefuseEngineType},
		{name: "type not joined", sel: sel{listed: "AWS::SQS::Queue", native: string(bucketID), as: "logs",
			typ: bucketType}, want: RefuseEngineType},
		{name: "address", sel: sel{listed: "AWS::S3::Bucket", native: string(bucketID), as: "a::b", typ: bucketType},
			want: RefuseAddress},
		{name: "hiding provider config", sel: bucketSel(),
			inputs: map[string]any{"ignoreTags": `{"keyPrefixes":["plunge-"]}`}, want: RefuseHiddenMarkers},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cloud := newLiveCloud()
			cloud.put(bucketID, map[string]any{"bucket": "logs"})
			f := newAdoptFixture(t, nil)
			opts, provs := indexOpts(cloud, testAdoption(c.sel), true)
			provs.inputs = c.inputs
			_, err := f.backend.Adopt(context.Background(), f.stack, opts)
			assert.Contains(t, refusalCodes(t, err), c.want)
			assert.Zero(t, f.store.total())
			assert.Zero(t, cloud.updateCount(bucketID))
		})
	}
}

// A stack whose stock checkpoint holds resources is the checkpoint adoption's to take.
func TestIndexAdoptionRefusesAStackWithAStockCheckpoint(t *testing.T) {
	t.Parallel()

	cloud := newLiveCloud()
	cloud.put(bucketID, map[string]any{"bucket": "logs"})
	f := newAdoptFixture(t, stockDeployment(t, false))
	opts, _ := indexOpts(cloud, testAdoption(bucketSel()), true)
	_, err := f.backend.Adopt(context.Background(), f.stack, opts)
	assert.Contains(t, refusalCodes(t, err), RefuseStockCheckpoint)
	assert.Zero(t, f.store.total())
}

// StackProviders configures the package's default provider from the stack's configuration, with the plugin's
// version, under the name the engine gives a default provider of that version.
func TestStackProvidersConfigureFromStackConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var configured resource.PropertyMap
	loader := deploytest.NewProviderLoader("aws", semver.MustParse("7.0.0"), func() (plugin.Provider, error) {
		return &deploytest.Provider{
			Version: semver.MustParse("7.0.0"),
			ConfigureF: func(_ context.Context, req plugin.ConfigureRequest) (plugin.ConfigureResponse, error) {
				configured = resource.ToResourcePropertyMap(req.Inputs)
				return plugin.ConfigureResponse{}, nil
			},
		}, nil
	})
	sink := diag.DefaultSink(io.Discard, io.Discard, diag.FormatOptions{Color: colors.Never})
	host := deploytest.NewPluginHost(sink, sink, nil, loader)
	pctx, err := plugin.NewContextWithHost(ctx, sink, sink, host, "", "", nil)
	require.NoError(t, err)
	defer func() { _ = pctx.Close() }()

	f := newAdoptFixture(t, nil)
	cfg := config.Map{
		config.MustMakeKey("aws", "region"):   config.NewValue("us-east-1"),
		config.MustMakeKey("other", "region"): config.NewValue("eu-west-1"),
	}
	sp, err := NewStackProviders(pctx, f.stack.Ref(), cfg, config.NopDecrypter)
	require.NoError(t, err)
	defer func() { _ = sp.Close() }()

	_, st, err := sp.Provider(ctx, "aws")
	require.NoError(t, err)
	assert.Equal(t, resource.NewURN("test", "test", "", "pulumi:providers:aws", "default_7_0_0"), st.URN)
	assert.NotEmpty(t, st.ID)
	assert.Equal(t, "us-east-1", st.Inputs["region"].StringValue())
	assert.Equal(t, "7.0.0", st.Inputs["version"].StringValue())
	assert.Equal(t, "us-east-1", configured["region"].StringValue(), "configured from the stack's aws config only")
	_, other := configured["other"]
	assert.False(t, other)
}
