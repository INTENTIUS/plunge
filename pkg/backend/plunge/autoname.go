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

// plunge's autonaming policy (INTENTIUS/plunge#7; the maintainer ruled option A on 2026-10-25). For a type whose
// names the cloud keeps unique, plunge gives the provider a deterministic name in place of upstream's random
// suffix, and forces delete-before-replace. Two runs creating one resource then ask the cloud for the same name;
// the cloud refuses the second, and the loser's re-run binds the winner's object by its markers (bind.go). That is
// claim C1 (docs/plunge/CONCURRENCY.md).
//
// The name is the URN's name, a dash, and seven hex digits of a SHA-256 over the project, the stack, the URN's
// qualified type and its name. It is the shape upstream's default autoname has (name, dash, seven hex), so it
// passes the same provider charsets, and it differs across stacks and projects, so two stacks sharing an account
// do not collide. Where the schema gives a maximum length, the name part is cut to fit and the hash is kept.
//
// Which types: those with schema evidence that the name is unique in its scope.
//
//   - Bridged (pulumi-aws, pulumi-azure, pulumi-gcp): the field the bridge's mapping marks autonamed, whose
//     Terraform argument forces a new resource, and which choudoufu's pkg/identity builds the type's import
//     identity from (or names as its UniqueName).
//   - aws-native: metadata.json's autoNamingSpec property, when it is in the type's primaryIdentifier.
//   - Anything else keeps upstream's autonaming, and its record says why C1 does not cover it.
//
// The user's explicit name, and the user's own `pulumi:autonaming` config, win. A user pattern with a random part
// on a covered type is refused by name unless EnvAllowRandomAutonames is set.
//
// The loser of a racing create is not retried inline. Its create fails with the cloud's already-exists error, and
// the next run binds the winner: by the winner's record when it landed, else by the marker-bind phase. An inline
// retry would have to recognise an already-exists error from every provider's wording and turn a failed create
// into an import inside the step executor; the re-run reaches the same state through code that already exists.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/awsnative"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// EnvAllowRandomAutonames, set to "true", lets a user `pulumi:autonaming` pattern with a random part (${hex(n)},
// ${alphanum(n)}, ${string(n)}, ${num(n)}, ${uuid}) stand on a type C1 covers. Those resources are then not
// covered by C1, and their records say so.
const EnvAllowRandomAutonames = "PLUNGE_ALLOW_RANDOM_AUTONAMES"

func randomAutonamesAllowed() bool {
	return strings.EqualFold(os.Getenv(EnvAllowRandomAutonames), "true")
}

// AutonameSource is where a resource's name came from in one run.
type AutonameSource string

const (
	// AutonamePlunge is plunge's deterministic name.
	AutonamePlunge AutonameSource = "plunge"
	// AutonameExplicit is a name the program set itself.
	AutonameExplicit AutonameSource = "explicit"
	// AutonameUser is the user's `pulumi:autonaming` config.
	AutonameUser AutonameSource = "user-config"
	// AutonameUpstream is upstream's autonaming, kept for a type C1 does not cover.
	AutonameUpstream AutonameSource = "upstream"
)

// autonameEntry is what a resource record says about the resource's name. It rides in the ownership entry.
type autonameEntry struct {
	By AutonameSource `json:"by"`
	// Property is the input the provider autonames.
	Property string `json:"property,omitempty"`
	// Name is the name plunge proposed, when it proposed one.
	Name string `json:"name,omitempty"`
	// C1 reports that the resource's name is deterministic and unique at the cloud, so two creates of it collide.
	C1 bool `json:"c1"`
	// Uncovered says why C1 does not cover the resource.
	Uncovered string `json:"uncovered,omitempty"`
}

// RandomAutonamePatternError refuses a user `pulumi:autonaming` pattern with a random part on a type C1 covers.
type RandomAutonamePatternError struct {
	URN         resource.URN
	Type        tokens.Type
	Expressions []string
}

func (e *RandomAutonamePatternError) Error() string {
	return fmt.Sprintf("plunge: refusing %s: the pulumi:autonaming pattern for %s has a random part (%s), so two "+
		"runs creating it would pick two names and both succeed, where plunge's own name makes the second collide "+
		"and bind the first (claim C1, docs/plunge/CONCURRENCY.md). Take the random part out of the pattern, or set "+
		"%s=true to keep it and give up C1 for this type", e.URN, e.Type, strings.Join(e.Expressions, ", "),
		EnvAllowRandomAutonames)
}

// autonamePolicy is the plunge backend's deploy.AutonamePolicy.
type autonamePolicy struct {
	catalog     *catalog
	ledger      *ledger
	allowRandom bool
}

var _ deploy.AutonamePolicy = (*autonamePolicy)(nil)

func newAutonamePolicy(c *catalog, l *ledger) *autonamePolicy {
	return &autonamePolicy{catalog: c, ledger: l, allowRandom: randomAutonamesAllowed()}
}

// Autoname implements deploy.AutonamePolicy.
func (p *autonamePolicy) Autoname(
	ctx context.Context, req deploy.AutonameRequest,
) (*plugin.AutonamingOptions, bool, error) {
	info := p.catalog.lookup(ctx, req.Provider, req.Type)
	covered, why := c1Covers(info)
	note := func(e autonameEntry) {
		e.Property = info.Autoname.Property
		if !e.C1 && e.Uncovered == "" {
			e.Uncovered = why
		}
		p.ledger.record(req.URN, func(o *ownershipEntry) { o.Autoname = &e })
	}

	if !covered {
		// Upstream's behaviour, whatever the user configured.
		by := AutonameUpstream
		if req.User != nil {
			by = AutonameUser
		}
		note(autonameEntry{By: by})
		return nil, false, nil
	}
	if explicitName(req.Inputs, info.Autoname.Property) {
		note(autonameEntry{By: AutonameExplicit, C1: true})
		return nil, false, nil
	}
	if req.User != nil {
		if len(req.UserRandom) == 0 {
			// Verbatim, disabled (the provider then refuses a resource with no name), or a pattern with no random
			// part: deterministic already, and upstream forces delete-before-replace for it itself.
			note(autonameEntry{By: AutonameUser, C1: true})
			return nil, false, nil
		}
		if !p.allowRandom {
			return nil, false, &RandomAutonamePatternError{URN: req.URN, Type: req.Type, Expressions: req.UserRandom}
		}
		note(autonameEntry{By: AutonameUser, Uncovered: fmt.Sprintf("the pulumi:autonaming pattern has a random "+
			"part (%s), kept because %s=true", strings.Join(req.UserRandom, ", "), EnvAllowRandomAutonames)})
		return nil, false, nil
	}

	name := DeterministicName(req.URN, info.Autoname.MaxLength, info.Autoname.Suffix)
	note(autonameEntry{By: AutonamePlunge, Name: name, C1: true})
	return &plugin.AutonamingOptions{
		ProposedName: name,
		// Propose, not enforce: the provider still applies its own transform and charset rules (a bridged type's
		// lowercasing, say), which are deterministic, and refuses a name its maximum length does not allow.
		Mode:            plugin.AutonamingModePropose,
		WarnIfNoSupport: true,
	}, true, nil
}

// c1Covers reports whether C1 covers info's type: a name the cloud keeps unique, and a marker the loser of a
// racing create can bind the winner by.
func c1Covers(info TypeInfo) (bool, string) {
	switch {
	case !info.Autoname.Unique:
		if info.Autoname.Uncovered != "" {
			return false, info.Autoname.Uncovered
		}
		return false, "the schema gives no evidence that this type's names are unique"
	case info.Shape != ShapeMap && info.Shape != ShapeKeyValueArray:
		return false, "the type carries no ownership marker, so the loser of a racing create has nothing to bind by"
	case info.Markerless:
		return false, "pkg/identity classes " + info.TFType + " as markerless, so the loser of a racing create has " +
			"nothing to bind by"
	}
	return true, ""
}

// explicitName reports whether the program set the autonamed property itself. An unknown value counts: the
// provider uses it once it is known, and autonaming does not apply.
func explicitName(inputs resource.PropertyMap, prop string) bool {
	if prop == "" {
		return false
	}
	v, ok := inputs[resource.PropertyKey(prop)]
	return ok && !unwrapValue(v).IsNull()
}

// autonameHashLen is how many hex digits of the hash a deterministic name carries, as many as upstream's default
// random suffix.
const autonameHashLen = 7

// DeterministicName is plunge's name for urn: the URN's name, a dash and seven hex digits of a SHA-256 over the
// project, the stack, the qualified type and the name. With maxLength above zero the result, plus suffix (which the
// provider appends), fits in it: the name part is cut at a character boundary and the hash is kept. It depends on
// nothing but its arguments, so every run on every machine gets the same name.
func DeterministicName(urn resource.URN, maxLength int, suffix string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"plunge-autoname/v1", string(urn.Project()), string(urn.Stack()), string(urn.QualifiedType()), urn.Name(),
	}, "\x00")))
	h := hex.EncodeToString(sum[:])[:autonameHashLen]
	name := urn.Name()
	if maxLength <= 0 {
		return name + "-" + h
	}
	room := maxLength - len(suffix) - len(h) - 1
	if room < 1 {
		// No room for any of the name: the hash alone, cut to fit.
		n := maxLength - len(suffix)
		if n < 1 {
			n = 1
		}
		if n > len(h) {
			n = len(h)
		}
		return h[:n]
	}
	if len(name) > room {
		cut := room
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = strings.TrimRight(name[:cut], "-_.")
		if name == "" {
			return h
		}
	}
	return name + "-" + h
}

// bridgedAutoname reads a bridged type's autonamed field from the bridge's mapping, and the uniqueness evidence
// for it from pkg/identity.
func bridgedAutoname(info *TypeInfo, fields map[string]*bridgeSchemaInfo, schema map[string]*bridgeSchema) {
	var named []string
	for tf, f := range fields {
		if f != nil && f.Default != nil && f.Default.AutoNamed {
			named = append(named, tf)
		}
	}
	if len(named) == 0 {
		info.Autoname.Uncovered = "the provider's mapping marks no property of " + info.TFType + " as autonamed"
		return
	}
	sort.Strings(named)
	tf := named[0]
	info.Autoname.Property = pulumiName(tf)
	if f := fields[tf]; f.Name != "" {
		info.Autoname.Property = f.Name
	}
	if s := schema[tf]; s == nil || !s.ForceNew {
		info.Autoname.Uncovered = fmt.Sprintf("%s's %s can change in place, so it is not taken as the type's "+
			"identity", info.TFType, tf)
		return
	}
	row := info.Identity
	if row == nil {
		info.Autoname.Uncovered = "pkg/identity has no row for " + info.TFType + ", so nothing says its " + tf +
			" is unique"
		return
	}
	if !row.ServerAssigned {
		for _, c := range row.Components {
			if c.Block == "" && contains(c.Attrs, tf) {
				info.Autoname.Unique = true
				info.Autoname.Evidence = "pkg/identity builds " + info.TFType + "'s import identity from " + tf
				return
			}
		}
	}
	if row.UniqueName.Set() && contains(row.UniqueName.Attrs, tf) {
		info.Autoname.Unique = true
		info.Autoname.Evidence = "pkg/identity names " + tf + " as " + info.TFType + "'s unique name"
		return
	}
	info.Autoname.Uncovered = fmt.Sprintf("pkg/identity's row for %s does not build its identity from %s or name it "+
		"unique", info.TFType, tf)
}

// nativeAutoname reads an aws-native type's autonamed property and bounds from its metadata.
func nativeAutoname(r awsnative.Resource, info *TypeInfo) {
	spec := r.AutoNamingSpec
	if spec == nil || spec.SDKName == "" {
		info.Autoname.Uncovered = r.CF + " has no autonamed property in the aws-native metadata"
		return
	}
	info.Autoname.Property = spec.SDKName
	info.Autoname.MaxLength = spec.MaxLength
	info.Autoname.Suffix = spec.Suffix()
	if !r.NameIsIdentifier() {
		info.Autoname.Uncovered = fmt.Sprintf("%s's primary identifier (%s) does not include its name %s", r.CF,
			strings.Join(r.PrimaryIdentifier, ", "), spec.SDKName)
		return
	}
	info.Autoname.Unique = true
	info.Autoname.Evidence = fmt.Sprintf("%s's primary identifier includes %s", r.CF, spec.SDKName)
}

// pulumiName is the bridge's default Pulumi name for a Terraform argument: snake_case to camelCase.
func pulumiName(tf string) string {
	parts := strings.Split(tf, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func contains(xs []string, x string) bool {
	for _, e := range xs {
		if e == x {
			return true
		}
	}
	return false
}
