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

package autonaming

import (
	"regexp"

	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// randomExpressionRegex matches every expression generateName fills from the random seed.
var randomExpressionRegex = regexp.MustCompile(`\${(?:hex|alphanum|string|num)\(\d+\)}|\${uuid}`)

// RandomExpressions returns the random expressions (${hex(8)}, ${uuid} and the like) in the pattern a resolves to
// for resources of type t, in the order they appear, and nil when that pattern has none or a does not resolve to a
// pattern at all. The plunge backend uses it to refuse, by name, a user pattern that would give two runs creating
// one resource two different names (INTENTIUS/plunge#7).
func RandomExpressions(a Autonamer, t tokens.Type) []string {
	if g, ok := a.(*Global); ok {
		a, _ = g.pluginOptionsForResourceType(t)
	}
	p, ok := a.(*Pattern)
	if !ok || p == nil {
		return nil
	}
	return randomExpressionRegex.FindAllString(p.Pattern, -1)
}
