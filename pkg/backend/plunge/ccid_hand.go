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

// The multi-part Cloud Control identifiers ccid.go's grammar rule cannot translate (INTENTIUS/plunge#19). Each of
// these taggable, listable pulumi-aws types has a Terraform import id that pkg/identity does not spell from the
// identifier's parts alone: it has no row, or its row is an ARN with the region and account in it, or it names the
// object by a part of an ARN. Each translation is written out from the Terraform provider's importer for the type.
//
// AWS::Budgets::BudgetsAction (aws_budgets_budget_action) is left untranslated: its Terraform id starts with the
// account ID ("account_id:action_id:budget_name"), which neither the identifier nor the Cloud Control model carries.

import (
	"fmt"
	"strings"
)

// ccHand is one written-out translation from a Cloud Control identifier (its parts, in primary identifier order) and,
// when prop is set, the resource model's prop, to the Terraform id.
type ccHand struct {
	tfType string
	// prop is a model property the id is read from (an ARN the list or read handler returns). Empty when the parts
	// are enough.
	prop  string
	arity int
	build func(parts []string, prop string) (string, bool)
}

// ccHands are the written-out translations, by CloudFormation type.
var ccHands = map[string]ccHand{
	// aws_eip is imported by its allocation ID ("eipalloc-..."); the identifier is "PublicIp|AllocationId".
	"AWS::EC2::EIP": {tfType: "aws_eip", arity: 2, build: func(p []string, _ string) (string, bool) {
		return p[1], strings.HasPrefix(p[1], "eipalloc-")
	}},
	// aws_ecs_service is imported as "<cluster name>/<service name>"; the identifier is "ServiceArn|Cluster", both
	// ARNs (arn:aws:ecs:<region>:<account>:service/<cluster>/<name>, older services service/<name>).
	"AWS::ECS::Service": {tfType: "aws_ecs_service", arity: 2, build: func(p []string, _ string) (string, bool) {
		service := p[0][strings.LastIndex(p[0], "/")+1:]
		cluster := p[1]
		if i := strings.Index(cluster, ":cluster/"); i >= 0 {
			cluster = cluster[i+len(":cluster/"):]
		}
		if service == "" || cluster == "" || strings.Contains(cluster, "/") {
			return "", false
		}
		return cluster + "/" + service, true
	}},
	// The GuardDuty detector children are imported as "<detector id>:<child id>".
	"AWS::GuardDuty::IPSet": {tfType: "aws_guardduty_ipset", arity: 2, build: func(p []string, _ string) (string, bool) {
		return p[1] + ":" + p[0], true // Id|DetectorId
	}},
	"AWS::GuardDuty::ThreatIntelSet": {tfType: "aws_guardduty_threatintelset", arity: 2,
		build: func(p []string, _ string) (string, bool) { return p[1] + ":" + p[0], true }}, // Id|DetectorId
	"AWS::GuardDuty::PublishingDestination": {tfType: "aws_guardduty_publishing_destination", arity: 2,
		build: func(p []string, _ string) (string, bool) { return p[0] + ":" + p[1], true }}, // DetectorId|Id
	// aws_quicksight_folder is imported as "<account id>,<folder id>"; the identifier is "AwsAccountId|FolderId".
	"AWS::QuickSight::Folder": {tfType: "aws_quicksight_folder", arity: 2,
		build: func(p []string, _ string) (string, bool) { return p[0] + "," + p[1], true }},
	// The SageMaker domain children are imported by their ARN, which pkg/identity spells with the region and the
	// account; the model carries the ARN itself.
	"AWS::SageMaker::App":   {tfType: "aws_sagemaker_app", prop: "AppArn", arity: 4, build: arnProp},
	"AWS::SageMaker::Space": {tfType: "aws_sagemaker_space", prop: "SpaceArn", arity: 2, build: arnProp},
	"AWS::SageMaker::UserProfile": {tfType: "aws_sagemaker_user_profile", prop: "UserProfileArn", arity: 2,
		build: arnProp},
}

func arnProp(_ []string, arn string) (string, bool) {
	return arn, strings.HasPrefix(arn, "arn:")
}

// ccHandTranslation is cfnType's written-out translation for tfType, when there is one.
func ccHandTranslation(tfType, cfnType string) (*ccTranslation, bool) {
	h, ok := ccHands[cfnType]
	if !ok || h.tfType != tfType {
		return nil, false
	}
	return &ccTranslation{arity: h.arity, hand: &h}, true
}

// needsModel reports whether t reads a model property the list handler may leave out, and which.
func (t *ccTranslation) needsModel() string {
	if t == nil || t.hand == nil {
		return ""
	}
	return t.hand.prop
}

// wantsModel reports whether the listing should read the resource before translating identifier, because the list
// handler's model lacks what the translation reads.
func (t *ccTranslation) wantsModel(identifier string, props map[string]any) bool {
	switch {
	case t == nil:
		return false
	case t.single != nil:
		return t.single.wantsModel(identifier, props)
	case t.hand != nil && t.hand.prop != "":
		_, has := props[t.hand.prop]
		return !has
	}
	return false
}

// describe names the id the translation produces, for an error message.
func (t *ccTranslation) describe(tfType string) string {
	if t.single != nil {
		return t.single.describe()
	}
	return fmt.Sprintf("%s's Terraform id (it should have %d parts)", tfType, t.arity)
}

// translate is the Terraform id of one listed object: identifier is its Cloud Control identifier, props its model.
func (t *ccTranslation) translate(identifier string, props map[string]any) (string, bool) {
	if t.single != nil {
		return t.single.translate(identifier, props)
	}
	if t.hand == nil {
		return t.apply(identifier)
	}
	parts := strings.Split(identifier, "|")
	if len(parts) != t.arity {
		return "", false
	}
	for _, p := range parts {
		if p == "" {
			return "", false
		}
	}
	prop := ""
	if t.hand.prop != "" {
		prop, _ = props[t.hand.prop].(string)
	}
	return t.hand.build(parts, prop)
}
