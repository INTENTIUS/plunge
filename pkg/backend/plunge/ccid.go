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

// Translating a multi-part Cloud Control identifier to the Terraform id a pulumi-aws resource is imported by
// (INTENTIUS/plunge#15). Cloud Control joins a type's primary identifier properties with "|", in the order the
// CloudFormation registry lists them (AWS::Cassandra::Table is "KeyspaceName|TableName"). pulumi-aws imports the
// Terraform id, whose grammar choudoufu's pkg/identity row spells as Components: arguments and separator literals
// ("keyspace_name" "/" "table_name").
//
// The translation is defined when every argument component of the row is one primary identifier property, matched
// by name (the property in snake case, "KeyspaceName" to "keyspace_name", or a rename in ccRenames), and every
// property is used exactly once. The id is then the components in the row's order with each property's part put in.
// A row with any other component (a block, a path, an optional or defaulted segment, a value of the cloud such as
// the region) is left untranslated, and the type stays unlisted with that reason, rather than guessing at an id.

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/intentius/choudoufu/pkg/identity"
)

// ccRenames are primary identifier properties whose Terraform argument is not the property's snake case, by
// CloudFormation type.
var ccRenames = map[string]map[string]string{
	"AWS::CodePipeline::CustomActionType": {"Provider": "provider_name"},
	"AWS::Kendra::DataSource":             {"Id": "data_source_id"},
	"AWS::Kendra::Faq":                    {"Id": "faq_id"},
}

// ccTranslation turns one Cloud Control identifier of a type into its Terraform id.
type ccTranslation struct {
	// Each output piece is literals[i] when index[i] < 0, else the identifier part at index[i].
	literals []string
	index    []int
	arity    int
	// hand is a written-out translation (ccid_hand.go), used in place of literals and index.
	hand *ccHand
	// single is a single-part identifier's translation (ccid_single.go), used in place of all of the above.
	single *ccSingle
}

func (t ccTranslation) apply(identifier string) (string, bool) {
	parts := strings.Split(identifier, "|")
	if len(parts) != t.arity {
		return "", false
	}
	var b strings.Builder
	for i, idx := range t.index {
		if idx < 0 {
			b.WriteString(t.literals[i])
			continue
		}
		if parts[idx] == "" {
			return "", false
		}
		b.WriteString(parts[idx])
	}
	return b.String(), true
}

// ccTerraformID builds the translation for tfType, listed through cfnType whose primary identifier is primary. The
// reason says why there is none.
func ccTerraformID(tfType, cfnType string, primary []string) (ccTranslation, string) {
	if len(primary) < 2 {
		return ccTranslation{}, fmt.Sprintf("%s's Cloud Control identifier has %d parts", cfnType, len(primary))
	}
	row, ok := identity.LookupType(tfType)
	if !ok || row.ServerAssigned || len(row.Components) == 0 {
		return ccTranslation{}, fmt.Sprintf("choudoufu's pkg/identity has no import id grammar for %s, so %s's "+
			"multi-part Cloud Control identifier cannot be turned into its Terraform id", tfType, cfnType)
	}
	byArg := map[string]int{}
	for i, p := range primary {
		arg := snakeCase(p)
		if r, ok := ccRenames[cfnType][p]; ok {
			arg = r
		}
		byArg[arg] = i
	}
	t := ccTranslation{arity: len(primary)}
	used := map[int]bool{}
	for _, c := range row.Components {
		if len(c.Attrs) == 0 {
			t.literals, t.index = append(t.literals, c.Literal), append(t.index, -1)
			continue
		}
		if c.Block != "" || len(c.Path) > 0 || c.OmitIfAbsent || c.Default != "" || c.Literal != "" {
			return ccTranslation{}, fmt.Sprintf("%s's import id has a segment plunge does not translate from a "+
				"Cloud Control identifier (%s)", tfType, strings.Join(c.Attrs, " or "))
		}
		idx, found := -1, false
		for _, a := range c.Attrs {
			if i, ok := byArg[a]; ok {
				idx, found = i, true
				break
			}
		}
		if !found || used[idx] {
			return ccTranslation{}, fmt.Sprintf("%s's import id segment %s is not one of %s's identifier "+
				"properties (%s)", tfType, strings.Join(c.Attrs, " or "), cfnType, strings.Join(primary, ", "))
		}
		used[idx] = true
		t.literals, t.index = append(t.literals, ""), append(t.index, idx)
	}
	if len(used) != len(primary) {
		return ccTranslation{}, fmt.Sprintf("%s's import id does not use every part of %s's identifier (%s)",
			tfType, cfnType, strings.Join(primary, ", "))
	}
	return t, ""
}

// snakeCase is a CloudFormation property name in Terraform's spelling: "KeyspaceName" is "keyspace_name",
// "VPCConnectionId" is "vpc_connection_id".
func snakeCase(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		if unicode.IsUpper(c) {
			if i > 0 && (unicode.IsLower(r[i-1]) || (i+1 < len(r) && unicode.IsLower(r[i+1]) &&
				unicode.IsUpper(r[i-1]))) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(c))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}
