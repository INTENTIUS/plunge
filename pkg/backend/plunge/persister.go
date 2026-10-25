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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/staterecord"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/logging"
)

// RecordConflictError is a conditional write the store refused: another run changed, created or deleted the record
// after this run read it. Nothing is overwritten. The run stops; a re-run reads the records as they now are and
// plans against them.
type RecordConflictError struct {
	// What names the record in words: "resource <urn>", "output <name>", "the secrets configuration".
	What string
	Key  string
	Err  *staterecord.VersionConflictError
}

func (e *RecordConflictError) Error() string {
	return fmt.Sprintf("plunge: the record for %s at %q changed under this run (%v); another run wrote it, so this "+
		"run wrote nothing over it. Re-run to plan against the records as they are now", e.What, e.Key, e.Err)
}

func (e *RecordConflictError) Unwrap() error { return e.Err }

// SecretsConfigError refuses a run whose secrets configuration differs from the one the stack's records were
// written with. The secrets record is created once and never rewritten.
type SecretsConfigError struct {
	Key          string
	RecordedType string
	RunType      string
}

func (e *SecretsConfigError) Error() string {
	return fmt.Sprintf("plunge: the stack's secrets configuration is fixed by the record at %q (type %q), and this run "+
		"uses a different one (type %q); plunge does not rewrite it. Use the recorded secrets provider, or migrate the "+
		"stack's secrets as a separate, deliberate step", e.Key, e.RecordedType, e.RunType)
}

// recordPersister is the backend.SnapshotPersister for the plunge backend. Each Save turns the whole deployment the
// snapshot manager hands it into per-record writes, and writes only the records whose content changed since this run
// last read or wrote them, each conditioned on the version it last saw: If-None-Match for a create, If-Match for an
// update, a versioned delete for a removal. A record no step touched is never rewritten, which is what lets two runs
// changing different resources of one stack both land.
type recordPersister struct {
	ctx    context.Context
	store  staterecord.Store
	keys   stackKeys
	run    Generation
	guard  bool // prune outputs and snippets only when their generation allows it
	crypto *plaintexts
	now    func() time.Time

	mu     sync.Mutex
	known  map[string]*known
	writes int // conditional writes and deletes issued, for tests and logs
}

var _ backend.SnapshotPersister = (*recordPersister)(nil)

func newRecordPersister(
	ctx context.Context, store staterecord.Store, l *loaded, run Generation, guard bool, dec config.Decrypter,
) *recordPersister {
	k := make(map[string]*known, len(l.records))
	for key, rec := range l.records {
		k[key] = rec
	}
	return &recordPersister{
		ctx:    ctx,
		store:  store,
		keys:   l.keys,
		run:    run,
		guard:  guard,
		crypto: newPlaintexts(dec),
		now:    time.Now,
		known:  k,
	}
}

// desired is one record a deployment implies.
type desired struct {
	what      string
	resource  *resourceRecord
	output    *outputRecord
	snippet   *snippetRecord
	extension *extensionRecord
}

// Save implements backend.SnapshotPersister.
func (p *recordPersister) Save(d apitype.TypedDeployment) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if d.Deployment == nil {
		return nil
	}
	if err := p.saveSecrets(d.Deployment.SecretsProviders); err != nil {
		return err
	}

	want := p.split(d)

	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := p.write(key, want[key]); err != nil {
			return err
		}
	}

	var gone []string
	for key, k := range p.known {
		if _, still := want[key]; still || k.secrets != nil {
			continue
		}
		gone = append(gone, key)
	}
	sort.Strings(gone)
	for _, key := range gone {
		if err := p.remove(key); err != nil {
			return err
		}
	}
	return nil
}

// split turns a deployment into the records it implies, keyed by store key.
func (p *recordPersister) split(d apitype.TypedDeployment) map[string]*desired {
	dep := d.Deployment
	want := map[string]*desired{}

	byURN := map[resource.URN]*resourceRecord{}
	var order []resource.URN
	rec := func(urn resource.URN) *resourceRecord {
		r, ok := byURN[urn]
		if !ok {
			r = &resourceRecord{Format: resourceFormat, URN: urn, Features: d.Features}
			byURN[urn] = r
			order = append(order, urn)
		}
		return r
	}

	for _, state := range dep.Resources {
		if isRootStack(state) {
			for name, v := range state.Outputs {
				want[p.keys.output(name)] = &desired{
					what:   fmt.Sprintf("output %q", name),
					output: &outputRecord{Format: outputFormat, Name: name, Value: v},
				}
			}
			state.Outputs = nil
		}
		r := rec(state.URN)
		r.States = append(r.States, state)
	}
	for _, op := range dep.PendingOperations {
		// Pending operations are kept for custom, non-provider resources only: those are the ones with a cloud
		// object a crash can leave half made. A provider or a component has nothing outside the records, and
		// keeping its pending create would stop two runs that both create the default provider or the root
		// Stack from converging on one record.
		if !op.Resource.Custom || providers.IsProviderType(op.Resource.Type) {
			continue
		}
		r := rec(op.Resource.URN)
		r.Pending = append(r.Pending, op)
	}
	for _, urn := range order {
		want[p.keys.resource(urn)] = &desired{what: "resource " + string(urn), resource: byURN[urn]}
	}
	for _, s := range dep.Snippets {
		want[p.keys.snippet(s.UUID)] = &desired{
			what:    fmt.Sprintf("snippet %q", s.UUID),
			snippet: &snippetRecord{Format: snippetFormat, Snippet: s},
		}
	}
	for ref, ext := range dep.Extensions {
		want[p.keys.extension(ref)] = &desired{
			what:      fmt.Sprintf("extension %q", ref),
			extension: &extensionRecord{Format: extensionFormat, Ref: ref, Extension: ext},
		}
	}
	return want
}

// content is what decides whether a record changed, in canonical form.
func (p *recordPersister) content(w *desired, k *known, ignoreTimes bool) ([]byte, []byte, error) {
	var a, b any
	switch {
	case w.resource != nil:
		a = resourceContent(w.resource, ignoreTimes)
		if k != nil && k.resource != nil {
			b = resourceContent(k.resource, ignoreTimes)
		}
	case w.output != nil:
		a = w.output.Value
		if k != nil && k.output != nil {
			b = k.output.Value
		}
	case w.snippet != nil:
		a = w.snippet.Snippet
		if k != nil && k.snippet != nil {
			b = k.snippet.Snippet
		}
	case w.extension != nil:
		a = w.extension.Extension
		if k != nil && k.extension != nil {
			b = k.extension.Extension
		}
	}
	ca, err := p.crypto.canonical(p.ctx, a)
	if err != nil {
		return nil, nil, err
	}
	if b == nil {
		return ca, nil, nil
	}
	cb, err := p.crypto.canonical(p.ctx, b)
	if err != nil {
		return nil, nil, err
	}
	return ca, cb, nil
}

// write brings one record to what the deployment says, if it differs from what this run last saw.
func (p *recordPersister) write(key string, w *desired) error {
	k := p.known[key]
	mine, theirs, err := p.content(w, k, false)
	if err != nil {
		return fmt.Errorf("plunge: comparing the record for %s: %w", w.what, err)
	}
	if theirs != nil && bytes.Equal(mine, theirs) {
		return nil // unchanged: never rewritten
	}

	var prev Generation
	expected := ""
	if k != nil {
		prev = k.generation()
		expected = k.version
	}
	stamp := maxGeneration(prev, p.run)
	writtenAt := p.now().UTC().Format(time.RFC3339)
	next := &known{}
	var payload any
	switch {
	case w.resource != nil:
		w.resource.Generation, w.resource.WrittenAt = stamp, writtenAt
		next.resource, payload = w.resource, w.resource
	case w.output != nil:
		w.output.Generation, w.output.WrittenAt = stamp, writtenAt
		next.output, payload = w.output, w.output
	case w.snippet != nil:
		w.snippet.Generation = stamp
		next.snippet, payload = w.snippet, w.snippet
	case w.extension != nil:
		next.extension, payload = w.extension, w.extension
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("plunge: encoding the record for %s: %w", w.what, err)
	}

	p.writes++
	version, err := p.store.PutIfVersion(p.ctx, key, encoded, expected)
	if err != nil {
		var conflict *staterecord.VersionConflictError
		if !errors.As(err, &conflict) {
			return fmt.Errorf("plunge: writing the record for %s at %q: %w", w.what, key, err)
		}
		// Another run wrote the record first. If what is there now says what this run would have written (two
		// runs creating the same default provider with the same derived ID, or the same root Stack), adopt it
		// and carry on. A record with a pending operation is never adopted: two runs both creating one custom
		// resource is a collision, not agreement.
		if w.resource == nil || len(w.resource.Pending) == 0 {
			if adopted, aerr := p.adopt(key, w); aerr != nil {
				return aerr
			} else if adopted {
				return nil
			}
		}
		return &RecordConflictError{What: w.what, Key: key, Err: conflict}
	}
	next.version = version
	p.known[key] = next
	logging.V(7).Infof("plunge: wrote %s at %q (version %s)", w.what, key, version)
	return nil
}

// adopt reads the record another run created first, and keeps it as this run's own when it is equivalent to what
// this run would have written (ignoring creation and modification times).
func (p *recordPersister) adopt(key string, w *desired) (bool, error) {
	payload, version, exists, err := p.store.Get(p.ctx, key)
	if err != nil {
		return false, fmt.Errorf("plunge: reading the record for %s at %q after a create conflict: %w", w.what, key, err)
	}
	if !exists {
		return false, nil
	}
	theirs := &known{version: version}
	switch {
	case w.resource != nil:
		var r resourceRecord
		if err := decodeRecord(key, payload, resourceFormat, &r); err != nil {
			return false, err
		}
		if len(r.Pending) > 0 {
			return false, nil
		}
		theirs.resource = &r
	case w.output != nil:
		var o outputRecord
		if err := decodeRecord(key, payload, outputFormat, &o); err != nil {
			return false, err
		}
		theirs.output = &o
	case w.snippet != nil:
		var s snippetRecord
		if err := decodeRecord(key, payload, snippetFormat, &s); err != nil {
			return false, err
		}
		theirs.snippet = &s
	case w.extension != nil:
		var e extensionRecord
		if err := decodeRecord(key, payload, extensionFormat, &e); err != nil {
			return false, err
		}
		theirs.extension = &e
	}
	mine, other, err := p.content(w, theirs, true)
	if err != nil {
		return false, err
	}
	if other == nil || !bytes.Equal(mine, other) {
		return false, nil
	}
	p.known[key] = theirs
	logging.V(7).Infof("plunge: adopted %s at %q, created by another run with the same content", w.what, key)
	return true, nil
}

// remove deletes a record the deployment no longer implies, conditioned on the version this run last saw.
func (p *recordPersister) remove(key string) error {
	k := p.known[key]
	what := key
	switch {
	case k.resource != nil:
		what = "resource " + string(k.resource.URN)
	case k.output != nil:
		what = fmt.Sprintf("output %q", k.output.Name)
		// An output a newer program wrote stays, as a resource does (generation.go).
		if p.guard && deleteRefusal(p.run, k.output.Generation) != "" {
			return nil
		}
	case k.snippet != nil:
		what = fmt.Sprintf("snippet %q", k.snippet.Snippet.UUID)
		if p.guard && deleteRefusal(p.run, k.snippet.Generation) != "" {
			return nil
		}
	}

	p.writes++
	err := p.store.Delete(p.ctx, key, k.version)
	if err != nil {
		var conflict *staterecord.VersionConflictError
		if !errors.As(err, &conflict) {
			return fmt.Errorf("plunge: deleting the record for %s at %q: %w", what, key, err)
		}
		if conflict.ActualVersion != "" {
			return &RecordConflictError{What: what, Key: key, Err: conflict}
		}
		// Already gone: another run deleted it too. The store agrees with this run.
	}
	delete(p.known, key)
	logging.V(7).Infof("plunge: deleted %s at %q", what, key)
	return nil
}

// saveSecrets creates the secrets record once, and refuses a run whose configuration differs from it.
func (p *recordPersister) saveSecrets(sp *apitype.SecretsProvidersV1) error {
	if sp == nil {
		return nil
	}
	key := p.keys.secrets()
	mine := &secretsRecord{Format: secretsFormat, Type: sp.Type, State: sp.State}
	if k, ok := p.known[key]; ok && k.secrets != nil {
		return p.sameSecrets(key, k.secrets, mine)
	}

	encoded, err := json.Marshal(mine)
	if err != nil {
		return fmt.Errorf("plunge: encoding the secrets configuration: %w", err)
	}
	p.writes++
	version, err := p.store.PutIfAbsent(p.ctx, key, encoded)
	if err == nil {
		p.known[key] = &known{version: version, secrets: mine}
		return nil
	}
	var conflict *staterecord.VersionConflictError
	if !errors.As(err, &conflict) {
		return fmt.Errorf("plunge: writing the secrets configuration at %q: %w", key, err)
	}
	payload, version, exists, err := p.store.Get(p.ctx, key)
	if err != nil {
		return fmt.Errorf("plunge: reading the secrets configuration at %q: %w", key, err)
	}
	if !exists {
		return &RecordConflictError{What: "the secrets configuration", Key: key, Err: conflict}
	}
	var theirs secretsRecord
	if err := decodeRecord(key, payload, secretsFormat, &theirs); err != nil {
		return err
	}
	if err := p.sameSecrets(key, &theirs, mine); err != nil {
		return err
	}
	p.known[key] = &known{version: version, secrets: &theirs}
	return nil
}

func (p *recordPersister) sameSecrets(key string, recorded, mine *secretsRecord) error {
	if recorded.Type != mine.Type {
		return &SecretsConfigError{Key: key, RecordedType: recorded.Type, RunType: mine.Type}
	}
	a, err := canonicalJSON(recorded.State)
	if err != nil {
		return err
	}
	b, err := canonicalJSON(mine.State)
	if err != nil {
		return err
	}
	if !bytes.Equal(a, b) {
		return &SecretsConfigError{Key: key, RecordedType: recorded.Type, RunType: mine.Type + " with a different state"}
	}
	return nil
}

func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
