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

// The acceptance test of INTENTIUS/plunge#27: a stack whose aws:endpoints point at localhost, with
// PLUNGE_CLOUDCONTROL_ENDPOINT unset, makes no request to a host that is not local.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// loopbackOnly sends requests for loopback hosts and fails the test on any other.
type loopbackOnly struct {
	t        *testing.T
	mu       sync.Mutex
	nonLocal []string
}

func (l *loopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Hostname()
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		l.mu.Lock()
		l.nonLocal = append(l.nonLocal, r.URL.Host)
		l.mu.Unlock()
		l.t.Errorf("a request went to the non-local host %s", r.URL.Host)
		return nil, fmt.Errorf("test: %s is not a loopback host", r.URL.Host)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func clearAWSEndpointEnv(t *testing.T) {
	for _, n := range append([]string{EnvCloudControlEndpoint, "AWS_ENDPOINT_URL"},
		envNamesWith("AWS_ENDPOINT_URL_", "")...) {
		t.Setenv(n, "")
	}
	t.Setenv("AWS_REGION", "us-east-1")
}

// Not parallel: it sets the environment and listerTransport.
func TestCloudControlListingKeepsToTheStacksEndpoints(t *testing.T) {
	clearAWSEndpointEnv(t)
	guard := &loopbackOnly{t: t}
	listerTransport = guard
	t.Cleanup(func() { listerTransport = nil })

	urn := resource.NewURN("floci", "test", "", keyspacesType, "events")
	tags := markedWith(t, urn, markers.EncodingTags, map[string]string{"team": "a"})
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		var tagList []map[string]string
		for k, v := range tags {
			tagList = append(tagList, map[string]string{"Key": k, "Value": v})
		}
		props, _ := json.Marshal(map[string]any{"KeyspaceName": "ks", "TableName": "events", "Tags": tagList})
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []map[string]string{
			{"Identifier": "ks|events", "Properties": string(props)},
		}})
	}))
	defer srv.Close()

	prov := mappingProvider("aws", "7.0.0", keyspacesMapping)
	cat := newCatalog()
	info := cat.lookup(context.Background(), prov, keyspacesType)
	inputs, merged := mergeMarkers(resource.NewPropertyMapFromMap(map[string]any{
		"keyspaceName": "ks", "tableName": "events", "tags": map[string]any{"team": "a"},
	}), info, markedWith(t, urn, markers.EncodingTags, nil))
	require.True(t, merged)

	jsonEndpoints := func(m map[string]string) string {
		b, err := json.Marshal([]map[string]string{m})
		require.NoError(t, err)
		return string(b)
	}
	cases := []struct {
		name      string
		endpoints any
		bound     bool
	}{
		// A default provider carries aws:endpoints as the config value's JSON text.
		{"a cloudcontrol entry", jsonEndpoints(map[string]string{"cloudcontrol": srv.URL,
			"s3": "http://localhost:1"}), true},
		{"one endpoint every service shares", jsonEndpoints(map[string]string{"s3": srv.URL, "sts": srv.URL,
			"ec2": srv.URL}), true},
		// An explicit provider carries the list itself.
		{"an explicit provider's list", []any{map[string]any{"cloudcontrol": srv.URL}}, true},
		{"custom endpoints and none for Cloud Control", []any{map[string]any{"s3": srv.URL,
			"sts": "http://localhost:1"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mu.Lock()
			calls = 0
			mu.Unlock()
			var out bytes.Buffer
			sink := diag.DefaultSink(&out, &out, diag.FormatOptions{Color: colors.Never})
			marks := newLedger()
			b := newBinder(cat, marks, map[Family]Lister{FamilyAWS: &cloudControlLister{}})
			id, err := b.Bind(context.Background(), deploy.BindRequest{
				URN: urn, Type: keyspacesType, Provider: prov, Inputs: inputs, Diag: sink,
				ProviderInputs: resource.NewPropertyMapFromMap(map[string]any{
					"region": "us-east-1", "endpoints": c.endpoints,
				}),
			})
			require.NoError(t, err)
			e, _ := marks.get(urn)
			mu.Lock()
			defer mu.Unlock()
			if c.bound {
				assert.Equal(t, resource.ID("ks/events"), id)
				assert.Equal(t, 1, calls, "the listing went to the stack's endpoint")
				return
			}
			assert.Empty(t, id)
			assert.Zero(t, calls, "nothing was listed")
			assert.Contains(t, e.Unlisted, "stack floci")
			assert.Contains(t, e.Unlisted, "no cloudcontrol entry")
			assert.Contains(t, out.String(), "warning")
			assert.Contains(t, out.String(), "stack floci")
		})
	}
	assert.Empty(t, guard.nonLocal)
}

// The sweep skips a listing the same way, with a warning in place of the note a failed listing gets.
func TestSweepWarnsWhenTheStacksEndpointsNameNoCloudControl(t *testing.T) {
	clearAWSEndpointEnv(t)
	listerTransport = &loopbackOnly{t: t}
	t.Cleanup(func() { listerTransport = nil })

	urn := bucketURN()
	info := TypeInfo{Type: bucketType, Family: FamilyAWS, Property: "tags", Shape: ShapeMap,
		CFNType: "AWS::S3::Bucket", Listable: true}
	marks := newLedger()
	marks.noteMarked(urn, info, &deploy.BindRequest{URN: urn, Type: bucketType,
		ProviderInputs: resource.NewPropertyMapFromMap(map[string]any{
			"endpoints": `[{"s3":"http://localhost:1","sts":"http://localhost:2"}]`,
		})})
	_, notes, err := sweepUnrecorded(context.Background(), newCountingStore(t), testKeys, marks,
		map[Family]Lister{FamilyAWS: &cloudControlLister{}})
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.True(t, notes[0].warn)
	assert.Contains(t, notes[0].text, "did not run")
}

// The other families keep to the same rule: a google provider with custom endpoints and none for the type's service
// lists nothing, nor does Asset Inventory without its own endpoint, and an azure provider reading its endpoints from
// a metadata host is not listed through the public ARM.
func TestOtherListersSkipWhenTheStackNamesOtherEndpoints(t *testing.T) {
	for _, n := range append(envNamesWith("GOOGLE_", "_CUSTOM_ENDPOINT"), "ARM_METADATA_HOSTNAME") {
		t.Setenv(n, "")
	}
	urn := resource.NewURN("floci", "test", "", "gcp:pubsub/topic:Topic", "events")
	req := deploy.BindRequest{URN: urn, ProviderInputs: resource.NewPropertyMapFromMap(map[string]any{
		"project": "plc", "accessToken": "fake", "storageCustomEndpoint": "http://localhost:4588/storage/v1/",
	})}
	l := newGCPLister()
	info := TypeInfo{Type: "gcp:pubsub/topic:Topic", Family: FamilyGoogle, TFType: "google_pubsub_topic"}
	_, err := l.ListMarked(context.Background(), info, req, map[string]string{markers.TagURN: "x"})
	var skip *SkippedListing
	require.ErrorAs(t, err, &skip)
	assert.Equal(t, "floci", skip.Stack)
	assert.Equal(t, "pubsub.googleapis.com", skip.Service)
	assert.Contains(t, l.Listing(info, req), "not listed")

	_, err = (&assetLister{}).ListMarked(context.Background(), info, req, map[string]string{markers.TagURN: "x"})
	require.ErrorAs(t, err, &skip)
	assert.Equal(t, "cloudasset.googleapis.com", skip.Service)

	azReq := deploy.BindRequest{URN: urn, ProviderInputs: resource.NewPropertyMapFromMap(map[string]any{
		"metadataHost": "localhost:4577", "subscriptionId": "00000000-0000-0000-0000-000000000000",
	})}
	_, err = (&armLister{}).ListMarked(context.Background(), TypeInfo{}, azReq, map[string]string{})
	require.ErrorAs(t, err, &skip)
	assert.Equal(t, "ARM", skip.Service)
	assert.Contains(t, skip.Error(), "localhost:4577")
}
