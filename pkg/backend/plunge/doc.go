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

// Package plunge is plunge's per-resource state backend (INTENTIUS/plunge#6).
//
// A stack's state is a set of records in a store with conditional writes (choudoufu's public staterecord package):
// one per URN, one per root Stack output, one immutable secrets configuration, one per snippet and extension. A
// run writes only the records it changed, each conditioned on the version it read, and holds no stack lock. The
// snapshot the engine sees is assembled from the records on every read, sorted so each resource follows what it
// refers to, and a dangling reference is refused by name.
//
// The backend is selected by a "plunge+" URL in front of a DIY backend URL. The DIY backend at the inner URL keeps
// everything that is not state (which stacks exist, their tags, the default secrets manager), unchanged, so the
// upstream diy and httpstate packages stay untouched.
//
// Two engine seams on deploy.Target carry the rest: ProviderIDs derives a new provider's ID instead of a random
// UUID (providerid.go), and DeleteGuard keeps a resource an older program version would delete (generation.go).
// DeleteVetter refuses a run, destroy included, that would delete an object whose live marker does not name the
// stack (deletevet.go, INTENTIUS/plunge#37), the delete half of a replacement included (INTENTIUS/plunge#51).
// ImportVetter holds an `import:` to the marker rules: a create-only-tags type, or an object another stack's marker
// names, is refused before the import step exists (importvet.go, INTENTIUS/plunge#51).
//
// docs/plunge/CONCURRENCY.md describes the behaviour for operators.
package plunge
