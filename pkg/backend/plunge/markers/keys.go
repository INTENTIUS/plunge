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

// Package markers is plunge's ownership-marker vocabulary (INTENTIUS/plunge#7): the tag keys, how a URN is carried
// in tag values and in label values, and reading them back off a live object.
//
// It is a leaf package. The marker injector and the bind and collision listers import it, and choudoufu's public
// sweeps (github.com/intentius/choudoufu/pkg/gcpsweep and the rest) are handed its keys and LabelValue through
// markerkeys.Keys (pkg/backend/plunge/sweepconfig.go), so every reader and writer means the same thing by a marker,
// the way choudoufu's internal/live/markers is the one implementation its own readers and writers share.
//
// Two encodings, one per family shape:
//
//   - Tags (AWS, aws-native, Azure): TagStack carries "<project>/<stack>" and TagURN the resource's URN with the
//     "urn:pulumi:<stack>::<project>::" prefix dropped, split over continuation keys when it is longer than one
//     tag value may be. Both read back as written.
//   - Labels (Google Cloud, Kubernetes): label values are short and lower-case, so both keys carry a hex digest
//     instead (16 characters for the stack, 32 for the URN). A digest is enough to bind and to detect a collision;
//     the URN itself is in the record.
package markers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

const (
	// Prefix starts every marker key. A provider setting that drops or overrides keys with a prefix this one
	// starts with, or a key it starts, hides the markers and is refused by name.
	Prefix = "plunge-"

	// TagStack names the stack that owns the resource: "<project>/<stack>".
	TagStack = "plunge-stack"

	// TagURN carries the owning resource's URN, without the stack and project (TagStack has those).
	TagURN = "plunge-urn"
)

// MaxTagValue is the longest tag value the tag families take (AWS and Azure both allow 256 characters).
const MaxTagValue = 256

// MaxContinuations is how many tag values one TagURN may span: TagURN, then TagURN-2 up to TagURN-4, as
// choudoufu's tofu-address continuation tags do.
const MaxContinuations = 4

// Encoding is how a family carries markers.
type Encoding int

const (
	// EncodingTags carries readable values (AWS, aws-native, Azure).
	EncodingTags Encoding = iota
	// EncodingLabels carries digests (Google Cloud labels, Kubernetes labels).
	EncodingLabels
)

// StackValue is the TagStack value for a project and stack.
func StackValue(project, stack string) string { return project + "/" + stack }

// shortURN is urn without its "urn:pulumi:<stack>::<project>::" prefix.
func shortURN(urn resource.URN) string {
	s := string(urn)
	prefix := "urn:pulumi:" + string(urn.Stack()) + "::" + string(urn.Project()) + "::"
	return strings.TrimPrefix(s, prefix)
}

func digest(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:n]
}

// ContinuationKey names the i-th (0-based) chunk's key: TagURN for 0, TagURN-<i+1> after.
func ContinuationKey(i int) string {
	if i == 0 {
		return TagURN
	}
	return fmt.Sprintf("%s-%d", TagURN, i+1)
}

// TooLongError refuses a URN too long to carry in MaxContinuations tag values.
type TooLongError struct {
	URN resource.URN
}

func (e *TooLongError) Error() string {
	return fmt.Sprintf("plunge: %s is too long to carry as an ownership marker (%d characters after the stack and "+
		"project, at most %d); shorten the resource name or its parents' types", e.URN, len([]rune(shortURN(e.URN))),
		MaxTagValue*MaxContinuations)
}

// For returns the markers one resource carries under enc.
func For(urn resource.URN, enc Encoding) (map[string]string, error) {
	project, stack := string(urn.Project()), string(urn.Stack())
	if enc == EncodingLabels {
		return map[string]string{
			TagStack: digest(StackValue(project, stack), 16),
			TagURN:   digest(string(urn), 32),
		}, nil
	}
	out := map[string]string{TagStack: StackValue(project, stack)}
	runes := []rune(shortURN(urn))
	if len(runes) > MaxTagValue*MaxContinuations {
		return nil, &TooLongError{URN: urn}
	}
	for i := 0; len(runes) > 0; i++ {
		n := min(len(runes), MaxTagValue)
		out[ContinuationKey(i)] = string(runes[:n])
		runes = runes[n:]
	}
	return out, nil
}

// LabelValue is the value key carries under the label encoding, for value as a caller names it: a TagStack value
// "<project>/<stack>" becomes its 16-character digest and a TagURN value "urn:pulumi:..." its 32-character digest,
// as For writes them. Any other value is taken to be a label spelling already and is returned as given; a digest is
// lower-case hex, so it never has the "/" of a stack value or the "urn:pulumi:" of a URN. It is the encoder plunge
// hands choudoufu's sweeps (markerkeys.Keys.Encode, INTENTIUS/plunge#39), so a caller may name either spelling.
func LabelValue(key, value string) string {
	switch {
	case key == TagStack && strings.Contains(value, "/"):
		return digest(value, 16)
	case key == TagURN && strings.HasPrefix(value, "urn:pulumi:"):
		return digest(value, 32)
	}
	return value
}

// IsMarkerKey reports whether key is one of plunge's marker keys (case-insensitively, since Azure keeps a key in
// its writer's casing and compares without it).
func IsMarkerKey(key string) bool {
	return strings.HasPrefix(strings.ToLower(key), Prefix)
}

// Carries reports whether tags hold every marker in want, with the same values.
func Carries(tags map[string]string, want map[string]string) bool {
	for k, v := range want {
		got, ok := lookupFold(tags, k)
		if !ok || got != v {
			return false
		}
	}
	return true
}

// Owner reads the owning stack and, for the tag encoding, the URN off tags. ok is false when tags carry no
// TagStack.
func Owner(tags map[string]string) (stack string, urn string, ok bool) {
	stack, ok = lookupFold(tags, TagStack)
	if !ok {
		return "", "", false
	}
	var b strings.Builder
	for i := 0; i < MaxContinuations; i++ {
		chunk, has := lookupFold(tags, ContinuationKey(i))
		if !has {
			break
		}
		b.WriteString(chunk)
	}
	return stack, b.String(), true
}

func lookupFold(tags map[string]string, key string) (string, bool) {
	if v, ok := tags[key]; ok {
		return v, true
	}
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

// HidesMarkers reports whether a provider's key or key-prefix setting (an ignore list, a default-tags map)
// would match a marker key: a key equal to one, or a prefix that one starts with, or that starts with Prefix.
func HidesMarkers(keyOrPrefix string, isPrefix bool) bool {
	k := strings.ToLower(keyOrPrefix)
	if k == "" {
		return isPrefix
	}
	if isPrefix {
		return strings.HasPrefix(Prefix, k) || strings.HasPrefix(k, Prefix)
	}
	return strings.HasPrefix(k, Prefix)
}
