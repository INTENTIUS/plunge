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

// INTENTIUS/plunge#28: single-part Cloud Control identifiers translated to pulumi-aws's id (ccid_single.go).
// Written for epic:consolidation-graph and not run in this unit.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/registry"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

func singleFor(t *testing.T, tfType, cfnType string) *ccTranslation {
	t.Helper()
	roster, err := registry.Embedded()
	require.NoError(t, err)
	primary := roster.PrimaryIdentifier(cfnType)
	require.Len(t, primary, 1, cfnType)
	tr, reason := ccSingleTranslation(tfType, cfnType, primary[0])
	require.Empty(t, reason, tfType)
	require.NotNil(t, tr, tfType)
	return tr
}

// Each table row names a pulumi-aws type joined to a CloudFormation type with a single-part identifier, as the
// registry has it, and no type is in two tables.
func TestSingleTranslationTablesMatchTheRegistry(t *testing.T) {
	t.Parallel()
	roster, err := registry.Embedded()
	require.NoError(t, err)
	seen := map[string]string{}
	check := func(table, tfType, cfnType string) {
		if prev, ok := seen[tfType]; ok {
			t.Errorf("%s is in %s and %s", tfType, prev, table)
		}
		seen[tfType] = table
		got, ok := roster.CloudControlType(tfType)
		if assert.True(t, ok, "%s (%s) has no Cloud Control type", tfType, table) {
			assert.Equal(t, cfnType, got, "%s (%s)", tfType, table)
		}
		assert.Equal(t, 1, roster.IdentifierArity(cfnType), "%s (%s)", cfnType, table)
	}
	for tf, p := range ccSingleProps {
		check("ccSingleProps", tf, p[0])
	}
	for tf, b := range ccSingleBuilds {
		check("ccSingleBuilds", tf, b.cfnType)
	}
	for tf := range ccSingleUncovered {
		cfn, ok := roster.CloudControlType(tf)
		require.True(t, ok, tf)
		check("ccSingleUncovered", tf, cfn)
	}
}

// Every type whose id is another model property reads it from the model, whatever the identifier, and asks for
// the resource when the list handler left the property out.
func TestSingleIdentifierReadsTheIDProperty(t *testing.T) {
	t.Parallel()
	for tf, p := range ccSingleProps {
		tr := singleFor(t, tf, p[0])
		identifier := "arn:aws:example:us-east-1:123456789012:thing/listed"
		assert.True(t, tr.wantsModel(identifier, map[string]any{}), tf)
		_, ok := tr.translate(identifier, map[string]any{})
		assert.False(t, ok, "%s: without the model's %s there is no id", tf, p[1])

		props := map[string]any{p[1]: "the-id"}
		assert.False(t, tr.wantsModel(identifier, props), tf)
		got, ok := tr.translate(identifier, props)
		require.True(t, ok, tf)
		assert.Equal(t, "the-id", got, tf)
	}
}

// Spot checks of ccSingleProps rows with real identifiers and models.
func TestSingleIdentifierTranslatedTypes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tfType, cfnType, identifier string
		props                       map[string]any
		want                        string
	}{
		{"aws_vpclattice_service", "AWS::VpcLattice::Service",
			"arn:aws:vpc-lattice:us-east-1:123456789012:service/svc-0abc",
			map[string]any{"Id": "svc-0abc", "Arn": "arn:aws:vpc-lattice:us-east-1:123456789012:service/svc-0abc"},
			"svc-0abc"},
		{"aws_kinesis_firehose_delivery_stream", "AWS::KinesisFirehose::DeliveryStream", "logs",
			map[string]any{"DeliveryStreamName": "logs",
				"Arn": "arn:aws:firehose:us-east-1:123456789012:deliverystream/logs"},
			"arn:aws:firehose:us-east-1:123456789012:deliverystream/logs"},
		{"aws_sagemaker_endpoint", "AWS::SageMaker::Endpoint",
			"arn:aws:sagemaker:us-east-1:123456789012:endpoint/infer",
			map[string]any{"EndpointName": "infer"}, "infer"},
		{"aws_dms_endpoint", "AWS::DMS::Endpoint", "arn:aws:dms:us-east-1:123456789012:endpoint:ABCDEF",
			map[string]any{"EndpointIdentifier": "source-db"}, "source-db"},
		{"aws_ecs_task_definition", "AWS::ECS::TaskDefinition",
			"arn:aws:ecs:us-east-1:123456789012:task-definition/web:3", map[string]any{"Family": "web"}, "web"},
		{"aws_amplify_app", "AWS::Amplify::App", "arn:aws:amplify:us-east-1:123456789012:apps/d1a2b3",
			map[string]any{"AppId": "d1a2b3"}, "d1a2b3"},
	} {
		tr := singleFor(t, tc.tfType, tc.cfnType)
		got, ok := tr.translate(tc.identifier, tc.props)
		require.True(t, ok, tc.tfType)
		assert.Equal(t, tc.want, got, tc.tfType)
	}
}

// The written-out single-part translations.
func TestSingleIdentifierBuiltIDs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, tfType, cfnType, identifier string
		props                             map[string]any
		want                              string
		ok                                bool
	}{
		{"rule on the default bus", "aws_cloudwatch_event_rule", "AWS::Events::Rule",
			"arn:aws:events:us-east-1:123456789012:rule/nightly", nil, "nightly", true},
		{"rule on the default bus, named", "aws_cloudwatch_event_rule", "AWS::Events::Rule",
			"arn:aws:events:us-east-1:123456789012:rule/default/nightly", nil, "nightly", true},
		{"rule on a custom bus", "aws_cloudwatch_event_rule", "AWS::Events::Rule",
			"arn:aws:events:us-east-1:123456789012:rule/orders/nightly", nil, "orders/nightly", true},
		{"rule identifier not an ARN", "aws_cloudwatch_event_rule", "AWS::Events::Rule", "nightly", nil, "", false},
		{"proxy endpoint", "aws_db_proxy_endpoint", "AWS::RDS::DBProxyEndpoint", "reader",
			map[string]any{"DBProxyName": "main"}, "main/reader", true},
		{"proxy endpoint without its proxy", "aws_db_proxy_endpoint", "AWS::RDS::DBProxyEndpoint", "reader",
			nil, "", false},
		{"guardrail", "aws_bedrock_guardrail", "AWS::Bedrock::Guardrail",
			"arn:aws:bedrock:us-east-1:123456789012:guardrail/gr1",
			map[string]any{"GuardrailId": "gr1", "Version": "DRAFT"}, "gr1,DRAFT", true},
		{"security lake subscriber", "aws_securitylake_subscriber", "AWS::SecurityLake::Subscriber",
			"arn:aws:securitylake:us-east-1:123456789012:subscriber/9f8e7d", nil, "9f8e7d", true},
	} {
		tr := singleFor(t, tc.tfType, tc.cfnType)
		got, ok := tr.translate(tc.identifier, tc.props)
		assert.Equal(t, tc.ok, ok, tc.name)
		if tc.ok {
			assert.Equal(t, tc.want, got, tc.name)
		}
	}
}

// A type whose id needs a value Cloud Control does not carry is uncovered, with the reason.
func TestSingleIdentifierUncoveredTypes(t *testing.T) {
	t.Parallel()
	roster, err := registry.Embedded()
	require.NoError(t, err)
	for tf := range ccSingleUncovered {
		cfn, ok := roster.CloudControlType(tf)
		require.True(t, ok, tf)
		tr, reason := ccSingleTranslation(tf, cfn, roster.PrimaryIdentifier(cfn)[0])
		assert.Nil(t, tr, tf)
		assert.NotEmpty(t, reason, tf)
		covered, why := (&cloudControlLister{}).Covers(TypeInfo{Type: "aws:x/y:Y", Family: FamilyAWS, TFType: tf,
			CFNType: cfn, Listable: true})
		assert.False(t, covered, tf)
		assert.Equal(t, reason, why, tf)
	}
}

// A primary identifier that is the id passes through; one of the wrong shape, as an emulator lists it, is
// repaired from the model or cut from the ARN, and kept as it is when neither can be done.
func TestSingleIdentifierOfTheWrongShape(t *testing.T) {
	t.Parallel()
	const fnARN = "arn:aws:lambda:us-east-1:000000000000:function:rec-r2-4507-handler"
	const queueURL = "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/events"
	for _, tc := range []struct {
		name, tfType, cfnType, identifier string
		props                             map[string]any
		wantsModel                        bool
		want                              string
	}{
		{"lambda by name", "aws_lambda_function", "AWS::Lambda::Function", "rec-r2-4507-handler", nil, false,
			"rec-r2-4507-handler"},
		{"lambda by ARN, model has the name", "aws_lambda_function", "AWS::Lambda::Function", fnARN,
			map[string]any{"FunctionName": "rec-r2-4507-handler"}, false, "rec-r2-4507-handler"},
		{"lambda by ARN, no model", "aws_lambda_function", "AWS::Lambda::Function", fnARN, nil, true,
			"rec-r2-4507-handler"},
		{"queue by URL", "aws_sqs_queue", "AWS::SQS::Queue", queueURL, nil, false, queueURL},
		{"queue by name, model has the URL", "aws_sqs_queue", "AWS::SQS::Queue", "events",
			map[string]any{"QueueUrl": queueURL}, false, queueURL},
		{"queue by name, no URL anywhere", "aws_sqs_queue", "AWS::SQS::Queue", "events", nil, true, "events"},
		{"topic by ARN", "aws_sns_topic", "AWS::SNS::Topic", "arn:aws:sns:us-east-1:000000000000:alerts", nil,
			false, "arn:aws:sns:us-east-1:000000000000:alerts"},
		{"bucket by name", "aws_s3_bucket", "AWS::S3::Bucket", "logs", nil, false, "logs"},
		{"role by ARN", "aws_iam_role", "AWS::IAM::Role", "arn:aws:iam::000000000000:role/svc/worker", nil, true,
			"worker"},
		{"security group by ID", "aws_security_group", "AWS::EC2::SecurityGroup", "sg-0abc", nil, false,
			"sg-0abc"},
		{"parameter by name", "aws_ssm_parameter", "AWS::SSM::Parameter", "/app/db/host", nil, false,
			"/app/db/host"},
	} {
		tr := singleFor(t, tc.tfType, tc.cfnType)
		assert.Equal(t, tc.wantsModel, tr.wantsModel(tc.identifier, tc.props), tc.name)
		got, ok := tr.translate(tc.identifier, tc.props)
		require.True(t, ok, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
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
