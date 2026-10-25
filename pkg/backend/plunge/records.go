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

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/staterecord"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/sig"
)

// The record formats. A record whose format is not one of these is refused by name, never guessed at.
const (
	resourceFormat  = "plunge-resource-v1"
	outputFormat    = "plunge-output-v1"
	secretsFormat   = "plunge-secrets-v1"
	snippetFormat   = "plunge-snippet-v1"
	extensionFormat = "plunge-extension-v1"
)

// keyEncoding turns a URN, an output name, a snippet UUID or an extension ref into one key segment. base64url
// without padding is inside the alphabet S3 object keys, blob names and file paths all accept, and has no "/".
var keyEncoding = base64.RawURLEncoding

// stackKeys is the key layout of one stack's records:
//
//	stacks/<project>/<stack>/secrets               the secrets configuration, written once (put-if-absent)
//	stacks/<project>/<stack>/resources/<urn>       every state and pending operation recorded for one URN
//	stacks/<project>/<stack>/outputs/<name>        one root Stack output
//	stacks/<project>/<stack>/snippets/<uuid>       one PCL snippet
//	stacks/<project>/<stack>/extensions/<ref>      one extension blob
//
// Nothing is shared between two URNs, two outputs or two snippets, which is what lets two runs that change different
// resources both land.
type stackKeys struct {
	ns string
}

func newStackKeys(project, stack string) stackKeys {
	return stackKeys{ns: staterecord.NamespacePrefix("stacks/" + project + "/" + stack)}
}

func (k stackKeys) prefix() string          { return k.ns }
func (k stackKeys) secrets() string         { return k.ns + "secrets" }
func (k stackKeys) resourcePrefix() string  { return k.ns + "resources/" }
func (k stackKeys) outputPrefix() string    { return k.ns + "outputs/" }
func (k stackKeys) snippetPrefix() string   { return k.ns + "snippets/" }
func (k stackKeys) extensionPrefix() string { return k.ns + "extensions/" }
func (k stackKeys) resource(urn resource.URN) string {
	return k.resourcePrefix() + keyEncoding.EncodeToString([]byte(urn))
}
func (k stackKeys) output(name string) string {
	return k.outputPrefix() + keyEncoding.EncodeToString([]byte(name))
}
func (k stackKeys) snippet(uuid string) string {
	return k.snippetPrefix() + keyEncoding.EncodeToString([]byte(uuid))
}
func (k stackKeys) extension(ref apitype.ExtensionRef) string {
	return k.extensionPrefix() + keyEncoding.EncodeToString([]byte(ref))
}

// decodeSegment returns what the last segment of key under prefix names, or false when key is not a record key of
// that kind.
func decodeSegment(key, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	b, err := keyEncoding.DecodeString(rest)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// Generation stamps a record with the program version that last wrote it: the commit and its commit time. It is
// what keeps a run of an older program from deleting a resource or an output a newer program wrote (see
// generation.go). Time is in Unix seconds; zero means the writer had no stamp.
type Generation struct {
	Commit string `json:"commit,omitempty"`
	Time   int64  `json:"time,omitempty"`
}

// newerThan reports whether g was written by a later program version than other.
func (g Generation) newerThan(other Generation) bool { return g.Time > other.Time }

// maxGeneration is the later of a and b. A record's generation never goes backwards: an older run updating a
// resource a newer run also declares leaves the newer stamp in place.
func maxGeneration(a, b Generation) Generation {
	if b.newerThan(a) {
		return b
	}
	return a
}

func (g Generation) String() string {
	if g.Time == 0 && g.Commit == "" {
		return "no program stamp"
	}
	commit := g.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if commit == "" {
		commit = "unknown commit"
	}
	return fmt.Sprintf("%s (commit time %d)", commit, g.Time)
}

// resourceRecord is what a resources/<urn> key holds: every state the snapshot has for that URN (the live one and
// any pending deletes left by a replacement, in snapshot order), and the pending operations on it. Pending
// operations live here, per resource, so there is no stack-wide pending list for two runs to fight over.
//
// For the root Stack resource, Outputs is always empty: each output is its own record (outputRecord).
type resourceRecord struct {
	Format     string                `json:"format"`
	URN        resource.URN          `json:"urn"`
	States     []apitype.ResourceV3  `json:"states,omitempty"`
	Pending    []apitype.OperationV2 `json:"pending,omitempty"`
	Features   []string              `json:"features,omitempty"`
	Generation Generation            `json:"generation"`
	WrittenAt  string                `json:"writtenAt,omitempty"`
}

// outputRecord is what an outputs/<name> key holds: one root Stack output, serialized exactly as it would be in the
// Stack resource's outputs (a secret stays a ciphertext envelope).
type outputRecord struct {
	Format     string     `json:"format"`
	Name       string     `json:"name"`
	Value      any        `json:"value"`
	Generation Generation `json:"generation"`
	WrittenAt  string     `json:"writtenAt,omitempty"`
}

// secretsRecord is the stack's secrets configuration. It is created once with put-if-absent and never rewritten; a
// run whose secrets configuration differs is refused by name.
type secretsRecord struct {
	Format string          `json:"format"`
	Type   string          `json:"type"`
	State  json.RawMessage `json:"state,omitempty"`
}

type snippetRecord struct {
	Format     string            `json:"format"`
	Snippet    apitype.SnippetV1 `json:"snippet"`
	Generation Generation        `json:"generation"`
}

type extensionRecord struct {
	Format    string               `json:"format"`
	Ref       apitype.ExtensionRef `json:"ref"`
	Extension apitype.Extension    `json:"extension"`
}

// isRootStack reports whether state is the root Stack resource whose outputs are split into records.
func isRootStack(state apitype.ResourceV3) bool {
	return state.Type == resource.RootStackType && state.Parent == "" && !state.Delete
}

// plaintexts decrypts secret envelopes for comparison, caching by ciphertext. A deployment re-serialized by the
// snapshot manager re-encrypts every secret once per run, and most crypters salt each encryption, so the same value
// arrives as a different ciphertext. Comparing records by plaintext is what lets an unchanged record stay unwritten.
type plaintexts struct {
	dec   config.Decrypter
	cache map[string]string
}

func newPlaintexts(dec config.Decrypter) *plaintexts {
	return &plaintexts{dec: dec, cache: map[string]string{}}
}

func (p *plaintexts) plaintext(ctx context.Context, ciphertext string) (string, error) {
	if p == nil || p.dec == nil {
		return "", errNoDecrypter
	}
	if v, ok := p.cache[ciphertext]; ok {
		return v, nil
	}
	v, err := p.dec.DecryptValue(ctx, ciphertext)
	if err != nil {
		return "", err
	}
	p.cache[ciphertext] = v
	return v, nil
}

var errNoDecrypter = fmt.Errorf("no decrypter")

// canonical returns the JSON encoding of v with every secret envelope replaced by its plaintext, and with object
// keys sorted (encoding/json sorts map keys). Two values that are the same up to secret re-encryption encode to the
// same bytes. When a ciphertext cannot be decrypted it is compared as is, which can only make a record look changed,
// never unchanged.
func (p *plaintexts) canonical(ctx context.Context, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	return json.Marshal(p.unseal(ctx, generic))
}

func (p *plaintexts) unseal(ctx context.Context, v any) any {
	switch v := v.(type) {
	case map[string]any:
		if s, ok := v[sig.Key].(string); ok && s == sig.Secret {
			if ct, ok := v["ciphertext"].(string); ok {
				if pt, err := p.plaintext(ctx, ct); err == nil {
					return map[string]any{sig.Key: sig.Secret, "plaintext": pt}
				}
			}
		}
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = p.unseal(ctx, e)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = p.unseal(ctx, e)
		}
		return out
	default:
		return v
	}
}

// resourceContent is the part of a resource record that says whether it changed: its states and pending
// operations. The generation, the write time and the feature list are bookkeeping and never make a record "changed"
// on their own.
func resourceContent(r *resourceRecord, ignoreTimes bool) any {
	if !ignoreTimes {
		return struct {
			States  []apitype.ResourceV3
			Pending []apitype.OperationV2
		}{r.States, r.Pending}
	}
	states := make([]apitype.ResourceV3, len(r.States))
	for i, s := range r.States {
		s.Created, s.Modified = nil, nil
		states[i] = s
	}
	return struct {
		States  []apitype.ResourceV3
		Pending []apitype.OperationV2
	}{states, r.Pending}
}
