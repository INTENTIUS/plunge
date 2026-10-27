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

// INTENTIUS/plunge#20: the S3 and GCS stores check their bucket's settings on open as the Azure Blob store does, and
// the in-flight refusal follows parent and deletedWith links as well as dependencies.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// s3Bare answers the three contract reads as a bucket with none of the settings: versioning never enabled, no
// lifecycle configuration and no public access block.
func s3Bare(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case q.Has("versioning"):
			_, _ = w.Write([]byte(`<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"/>`))
		case q.Has("lifecycle"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<Error><Code>NoSuchLifecycleConfiguration</Code><Message>none</Message></Error>`))
		case q.Has("publicAccessBlock"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<Error><Code>NoSuchPublicAccessBlockConfiguration</Code>` +
				`<Message>none</Message></Error>`))
		default:
			w.WriteHeader(http.StatusNotImplemented)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func bareS3Store(t *testing.T) staterecord.Store {
	srv := s3Bare(t)
	client := s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
	})
	store, err := staterecord.NewS3Store(staterecord.S3Config{Client: client, Bucket: "records", KeyPrefix: recordsDir})
	require.NoError(t, err)
	return store
}

func TestS3StoreLackingRequiredSettingsIsRefusedByName(t *testing.T) {
	t.Parallel()
	err := enforceContract(context.Background(), bareS3Store(t), nil)
	var refused *ContractRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, []staterecord.Setting{
		staterecord.BucketVersioning, staterecord.BucketLifecycle, staterecord.BucketPublicAccessBlock,
	}, refused.Settings)
	assert.Contains(t, err.Error(), "Bucket records")
}

func TestS3StoreWaivedSettingsOpen(t *testing.T) {
	t.Parallel()
	require.NoError(t, enforceContract(context.Background(), bareS3Store(t),
		[]string{"versioning", "lifecycle", "public_access_block"}))
}

// gcsBare answers the bucket read as a bucket with no versioning, no lifecycle rule, public access prevention
// inherited and fine-grained access, and an empty object listing.
func gcsBare(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/o"):
			_, _ = w.Write([]byte(`{"kind": "storage#objects", "items": []}`))
		case strings.Contains(r.URL.Path, "/b/records"):
			_, _ = w.Write([]byte(`{"kind": "storage#bucket", "name": "records", "iamConfiguration": ` +
				`{"publicAccessPrevention": "inherited", "uniformBucketLevelAccess": {"enabled": false}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGCSStoreLackingRequiredSettingsIsRefusedByName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := gcsBare(t)
	client, err := storage.NewClient(ctx, option.WithEndpoint(srv.URL+"/storage/v1/"), option.WithoutAuthentication())
	require.NoError(t, err)
	store, err := staterecord.NewGCSStore(staterecord.GCSConfig{Client: client, Bucket: "records", KeyPrefix: recordsDir})
	require.NoError(t, err)

	err = enforceContract(ctx, store, nil)
	var refused *ContractRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, []staterecord.Setting{
		staterecord.GCSObjectVersioning, staterecord.GCSNoncurrentLifecycle, staterecord.GCSPublicAccessPrevention,
		staterecord.GCSUniformBucketLevelAccess,
	}, refused.Settings, "list_isolation warns and is not refused")
}

// B begins an update of a resource whose parent, or whose deletedWith resource, has run A's create in flight. The
// save that begins it is refused naming the link, so the provider is never called for it.
func TestInFlightRefusalFollowsParentAndDeletedWith(t *testing.T) {
	t.Parallel()
	urnX := resource.URN("urn:pulumi:test::test::pkgA:m:typA::resX")
	urnC := resource.URN("urn:pulumi:test::test::pkgA:m:typA$pkgA:m:typA::resC")
	for _, tc := range []struct {
		name string
		link string
		set  func(*apitype.ResourceV3)
	}{
		{"parent", linkChildOf, func(s *apitype.ResourceV3) { s.Parent = urnX }},
		{"deletedWith", linkDeletedWith, func(s *apitype.ResourceV3) { s.DeletedWith = urnX }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := newCountingStore(t)
			x := apitype.ResourceV3{URN: urnX, Type: "pkgA:m:typA", Custom: true, ID: "x-1", Outputs: map[string]any{"v": "1"}}
			c := apitype.ResourceV3{URN: urnC, Type: "pkgA:m:typA", Custom: true, ID: "c-1", Inputs: map[string]any{"v": "1"}}
			tc.set(&c)

			l, err := load(ctx, store, testKeys)
			require.NoError(t, err)
			base := newRecordPersister(ctx, store, l, Generation{}, true, nil)
			require.NoError(t, base.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
				Resources: []apitype.ResourceV3{x, c},
			}}))

			// B reads the records, then A begins a create on resX.
			l, err = load(ctx, store, testKeys)
			require.NoError(t, err)
			startInFlightCreate(t, store, urnX)

			b := newRecordPersister(ctx, store, l, Generation{}, true, nil)
			c2 := c
			c2.Inputs = map[string]any{"v": "2"}
			err = b.Save(apitype.TypedDeployment{Deployment: &apitype.DeploymentV3{
				Resources:         []apitype.ResourceV3{x, c},
				PendingOperations: []apitype.OperationV2{{Resource: c2, Type: apitype.OperationTypeUpdating}},
			}})
			var inflight *InFlightDependencyError
			require.ErrorAs(t, err, &inflight)
			assert.Equal(t, urnC, inflight.URN)
			assert.Equal(t, urnX, inflight.Dependency)
			assert.Equal(t, tc.link, inflight.Link)
			assert.Contains(t, err.Error(), tc.link)
		})
	}
}

func TestStateLinksLeaveOutTheProvider(t *testing.T) {
	t.Parallel()
	s := apitype.ResourceV3{
		URN:          "urn:pulumi:test::test::pkgA:m:typA::c",
		Parent:       "urn:pulumi:test::test::pkgA:m:typA::p",
		DeletedWith:  "urn:pulumi:test::test::pkgA:m:typA::d",
		Dependencies: []resource.URN{"urn:pulumi:test::test::pkgA:m:typA::p"},
		Provider:     "urn:pulumi:test::test::pulumi:providers:pkgA::default::prov-1",
	}
	assert.Equal(t, []stateLink{
		{URN: "urn:pulumi:test::test::pkgA:m:typA::p", Link: linkDependsOn},
		{URN: "urn:pulumi:test::test::pkgA:m:typA::d", Link: linkDeletedWith},
	}, stateLinks(s))
}
