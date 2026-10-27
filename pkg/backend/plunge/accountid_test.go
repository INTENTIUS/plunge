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

// INTENTIUS/plunge#31: aws_budgets_budget_action's id takes the account ID from STS GetCallerIdentity on the
// stack's own endpoint. Every test here talks to a loopback fake only, under the loopbackOnly transport guard.
// Written for epic:consolidation-graph and not run in this unit.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

const budgetActionType = "aws:budgets/budgetAction:BudgetAction"

// fakeAWS is a loopback Cloud Control and STS: it lists one budgets action carrying tags, and answers
// GetCallerIdentity with account 123456789012.
type fakeAWS struct {
	mu        sync.Mutex
	stsCalls  int
	ccCalls   int
	unhandled []string
	tags      map[string]string
}

func (f *fakeAWS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		f.ccCalls++
		var tagList []map[string]string
		for k, v := range f.tags {
			tagList = append(tagList, map[string]string{"Key": k, "Value": v})
		}
		props, _ := json.Marshal(map[string]any{"ActionId": "act-1", "BudgetName": "monthly",
			"ResourceTags": tagList, "Tags": tagList})
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []map[string]string{
			{"Identifier": "act-1|monthly", "Properties": string(props)},
		}})
		return
	}
	form, _ := url.ParseQuery(string(body))
	if form.Get("Action") == "GetCallerIdentity" {
		f.stsCalls++
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">`+
			`<GetCallerIdentityResult><Arn>arn:aws:iam::123456789012:user/test</Arn><UserId>AIDATEST</UserId>`+
			`<Account>123456789012</Account></GetCallerIdentityResult>`+
			`<ResponseMetadata><RequestId>req-1</RequestId></ResponseMetadata></GetCallerIdentityResponse>`)
		return
	}
	f.unhandled = append(f.unhandled, r.Method+" "+r.URL.String()+" "+string(body))
	http.Error(w, "unexpected call", http.StatusBadRequest)
}

func budgetActionInfo() TypeInfo {
	return TypeInfo{Type: budgetActionType, Family: FamilyAWS, TFType: "aws_budgets_budget_action",
		CFNType: "AWS::Budgets::BudgetsAction", Listable: true, Property: "tags", Shape: ShapeMap}
}

func setupFakeAWS(t *testing.T) (*fakeAWS, *httptest.Server, resource.URN) {
	t.Helper()
	clearAWSEndpointEnv(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_SESSION_TOKEN", "")
	guard := &loopbackOnly{t: t}
	listerTransport = guard
	t.Cleanup(func() { listerTransport = nil })
	urn := resource.NewURN("floci", "test", "", budgetActionType, "alert")
	f := &fakeAWS{tags: markedWith(t, urn, markers.EncodingTags, nil)}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv, urn
}

func listBudgetActions(
	t *testing.T, l *cloudControlLister, urn resource.URN, providerInputs map[string]any,
) ([]LiveObject, error) {
	t.Helper()
	want := markedWith(t, urn, markers.EncodingTags, nil)
	return l.ListMarked(context.Background(), budgetActionInfo(), deploy.BindRequest{
		URN: urn, Type: tokens.Type(budgetActionType),
		ProviderInputs: resource.NewPropertyMapFromMap(providerInputs),
	}, map[string]string{markers.TagStack: want[markers.TagStack]})
}

// Not parallel: it sets the environment and listerTransport.
func TestBudgetsActionTakesTheAccountFromTheStacksSTS(t *testing.T) {
	f, srv, urn := setupFakeAWS(t)
	l := &cloudControlLister{}
	covered, reason := l.Covers(budgetActionInfo())
	require.True(t, covered, reason)

	inputs := map[string]any{"region": "us-east-1",
		"endpoints": []any{map[string]any{"cloudcontrol": srv.URL, "sts": srv.URL}}}
	for i := 0; i < 2; i++ {
		objs, err := listBudgetActions(t, l, urn, inputs)
		require.NoError(t, err)
		require.Len(t, objs, 1)
		assert.Equal(t, resource.ID("123456789012:act-1:monthly"), objs[0].ID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, 1, f.stsCalls, "the account ID is read once per endpoint, region and credentials")
	assert.Equal(t, 2, f.ccCalls)
	assert.Empty(t, f.unhandled)
}

// Not parallel: it sets the environment and listerTransport.
func TestBudgetsActionSkipsWithoutAnSTSEndpoint(t *testing.T) {
	f, srv, urn := setupFakeAWS(t)

	for _, tc := range []struct {
		name   string
		inputs map[string]any
		env    map[string]string
		reason string
	}{
		{"custom endpoints and none for STS", map[string]any{"region": "us-east-1",
			"endpoints": []any{map[string]any{"cloudcontrol": srv.URL, "s3": srv.URL}}}, nil, "no sts entry"},
		{"only Cloud Control pointed at the emulator", map[string]any{"region": "us-east-1"},
			map[string]string{EnvCloudControlEndpoint: srv.URL}, EnvCloudControlEndpoint},
		{"a service endpoint variable and none for STS", map[string]any{"region": "us-east-1"},
			map[string]string{"AWS_ENDPOINT_URL_CLOUDCONTROL": srv.URL}, "AWS_ENDPOINT_URL_CLOUDCONTROL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := listBudgetActions(t, &cloudControlLister{}, urn, tc.inputs)
			var skip *SkippedListing
			require.True(t, errors.As(err, &skip), "want a skipped listing, got %v", err)
			assert.Equal(t, "AWS STS", skip.Service)
			assert.Equal(t, "floci", skip.Stack)
			assert.Contains(t, skip.Reason, tc.reason)
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Zero(t, f.stsCalls, "no STS call is made at all")
	assert.Zero(t, f.ccCalls, "and the listing is not made")
}

// Not parallel: it sets the environment and listerTransport.
func TestBudgetsActionAccountFromAllowedAccountIDs(t *testing.T) {
	f, srv, urn := setupFakeAWS(t)
	objs, err := listBudgetActions(t, &cloudControlLister{}, urn, map[string]any{"region": "us-east-1",
		"allowedAccountIds": `["210987654321"]`,
		"endpoints":         []any{map[string]any{"cloudcontrol": srv.URL}}})
	require.NoError(t, err)
	require.Len(t, objs, 1)
	assert.Equal(t, resource.ID("210987654321:act-1:monthly"), objs[0].ID)
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Zero(t, f.stsCalls, "one allowed account is the account; no STS call")
}

// Where STS is called, by the endpoint rules of INTENTIUS/plunge#27.
func TestSTSEndpoint(t *testing.T) {
	clearAWSEndpointEnv(t)
	for _, tc := range []struct {
		name           string
		inputs         map[string]any
		env            map[string]string
		endpoint, skip string
	}{
		{"nothing custom: the public endpoint", map[string]any{"region": "eu-west-1"}, nil, "", ""},
		{"an sts entry", map[string]any{"endpoints": `[{"sts":"http://localhost:4566","s3":"http://localhost:1"}]`},
			nil, "http://localhost:4566", ""},
		{"an explicit provider's list", map[string]any{"endpoints": []any{map[string]any{"STS": "http://127.0.0.1:9"}}},
			nil, "http://127.0.0.1:9", ""},
		{"AWS_ENDPOINT_URL_STS", map[string]any{}, map[string]string{"AWS_ENDPOINT_URL_STS": "http://localhost:7"},
			"http://localhost:7", ""},
		{"AWS_ENDPOINT_URL", map[string]any{}, map[string]string{"AWS_ENDPOINT_URL": "http://localhost:8"},
			"http://localhost:8", ""},
		{"endpoints without sts", map[string]any{"endpoints": `[{"s3":"http://localhost:1"}]`}, nil, "",
			"no sts entry"},
		{"PLUNGE_CLOUDCONTROL_ENDPOINT alone", map[string]any{},
			map[string]string{EnvCloudControlEndpoint: "http://localhost:4566"}, "", EnvCloudControlEndpoint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			endpoint, skip := stsEndpoint(resource.NewPropertyMapFromMap(tc.inputs))
			assert.Equal(t, tc.endpoint, endpoint)
			if tc.skip == "" {
				assert.Empty(t, skip)
			} else {
				assert.True(t, strings.Contains(skip, tc.skip), skip)
			}
		})
	}
}
