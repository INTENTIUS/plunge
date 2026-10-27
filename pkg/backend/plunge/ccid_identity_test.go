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

// plunge's side of the Cloud Control identifier translation, which is choudoufu's pkg/identity since choudoufu#2286
// (its own tests moved with it): the lister covers a type exactly when the translation does, with its reason, and
// hands the binder and the sweep the translated id. Written for epic:consolidation-graph and not run in this unit.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/intentius/choudoufu/pkg/identity"
	"github.com/intentius/choudoufu/pkg/registry"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// A type pkg/identity cannot translate is not covered, and Covers gives pkg/identity's reason word for word; a type
// it translates is covered (INTENTIUS/plunge#28's uncovered single-part types, #15's multi-part rule).
func TestCloudControlCoverageFollowsTheTranslation(t *testing.T) {
	t.Parallel()
	roster, err := registry.Embedded()
	require.NoError(t, err)
	for _, tf := range []string{
		"aws_codepipeline_webhook", "aws_ecs_capacity_provider", "aws_elastic_beanstalk_environment",
		"aws_kinesisanalyticsv2_application", "aws_s3control_storage_lens_configuration",
	} {
		cfn, ok := roster.CloudControlType(tf)
		require.True(t, ok, tf)
		tr, reason := identity.CloudControlTranslation(tf, cfn, roster.PrimaryIdentifier(cfn))
		assert.Nil(t, tr, tf)
		require.NotEmpty(t, reason, tf)
		covered, why := (&cloudControlLister{}).Covers(TypeInfo{Type: "aws:x/y:Y", Family: FamilyAWS, TFType: tf,
			CFNType: cfn, Listable: true})
		assert.False(t, covered, tf)
		assert.Equal(t, reason, why, tf)
	}
	for _, tc := range [][2]string{
		{"aws_s3_bucket", "AWS::S3::Bucket"},
		{"aws_keyspaces_table", "AWS::Cassandra::Table"},
		{"aws_budgets_budget_action", "AWS::Budgets::BudgetsAction"},
	} {
		covered, why := (&cloudControlLister{}).Covers(TypeInfo{Type: "aws:x/y:Y", Family: FamilyAWS,
			TFType: tc[0], CFNType: tc[1], Listable: true})
		assert.True(t, covered, "%s: %s", tc[0], why)
	}
}

// The Cloud Control lister hands the binder and the sweep pulumi-aws's id: a function an emulator lists by its ARN
// comes back by its name, read from the model GetResource returns.
func TestCloudControlListerNamesAFunctionListedByARN(t *testing.T) {
	const fnType = "aws:lambda/function:Function"
	urn := resource.NewURN("test", "test", "", fnType, "handler")
	tags := markedWith(t, urn, markers.EncodingTags, nil)
	var tagList []map[string]string
	for k, v := range tags {
		tagList = append(tagList, map[string]string{"Key": k, "Value": v})
	}
	const arn = "arn:aws:lambda:us-east-1:000000000000:function:rec-r2-4507-handler"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Amz-Target") {
		case "CloudApiService.ListResources":
			_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []map[string]string{
				{"Identifier": arn, "Properties": "{}"},
			}})
		case "CloudApiService.GetResource":
			props, _ := json.Marshal(map[string]any{"FunctionName": "rec-r2-4507-handler", "Arn": arn,
				"Tags": tagList})
			_ = json.NewEncoder(w).Encode(map[string]any{"TypeName": "AWS::Lambda::Function",
				"ResourceDescription": map[string]string{"Identifier": arn, "Properties": string(props)}})
		default:
			http.Error(w, "unexpected call", http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	t.Setenv(EnvCloudControlEndpoint, srv.URL)
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	info := TypeInfo{Type: tokens.Type(fnType), Family: FamilyAWS, TFType: "aws_lambda_function",
		CFNType: "AWS::Lambda::Function", Listable: true, Property: "tags", Shape: ShapeMap}
	l := &cloudControlLister{}
	covered, reason := l.Covers(info)
	require.True(t, covered, reason)
	objs, err := l.ListMarked(context.Background(), info, deploy.BindRequest{URN: urn, Type: tokens.Type(fnType)},
		map[string]string{markers.TagStack: tags[markers.TagStack]})
	require.NoError(t, err)
	require.Len(t, objs, 1)
	assert.Equal(t, resource.ID("rec-r2-4507-handler"), objs[0].ID)
}
