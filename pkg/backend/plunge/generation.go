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

// Delete generation limited to what a run owns (INTENTIUS/plunge#6).
//
// Stock Pulumi deletes every resource in the base snapshot that the program did not register. With records and no
// stack lock, a run of an older program version can start after a newer run created a resource the older program
// has never heard of, and would delete it. choudoufu keeps that case outside its claims ("deletes from a run on an
// older commit"); plunge's acceptance asks for it to be refused.
//
// The mechanism is a per-record program generation: every record carries the stamp (commit and commit time) of the
// program version that last wrote it, never moving backwards (maxGeneration). A run may delete a resource, or prune
// an output or a snippet, only when the record's generation is not newer than the run's own. A resource it may not
// delete stays, as an untargeted same step, with a warning naming it and both stamps (deploy.Target.DeleteGuard).
//
// Why a commit time and not git ancestry: ancestry needs the record's commit in the local clone, and CI checkouts
// are often shallow, so the answer would depend on how the runner fetched. A commit time is in the record itself and
// is total. Its limit is clock skew between committers; two commits with equal times are treated as the same
// generation, which allows the delete. A run with no stamp at all (not a git checkout, no PLUNGE_PROGRAM_* set) can
// delete only records that carry no stamp either, so mixing stamped and unstamped runs errs towards keeping.
//
// Destroy is exempt: `destroy` is an explicit request to remove the stack's resources, not a judgement drawn from a
// program's registrations, so the backend sets no generation guard for it. Destroy is not exempt from the marker
// check: every delete, a destroy's included, is vetted against the live object's marker first (deletevet.go,
// INTENTIUS/plunge#37).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	pkgresource "github.com/pulumi/pulumi/pkg/v3/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// The environment variables that set a run's program stamp explicitly. Both override git.
const (
	EnvProgramCommit = "PLUNGE_PROGRAM_COMMIT"
	EnvProgramTime   = "PLUNGE_PROGRAM_TIME"
)

// ProgramGeneration returns the stamp of the program in root: PLUNGE_PROGRAM_COMMIT and PLUNGE_PROGRAM_TIME when set,
// otherwise the HEAD commit and its committer time from git. It returns the zero stamp, and no error, when root is
// not in a git checkout.
func ProgramGeneration(ctx context.Context, root string, getenv func(string) string) (Generation, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	var g Generation
	if t := getenv(EnvProgramTime); t != "" {
		secs, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			return Generation{}, fmt.Errorf("plunge: %s=%q is not a Unix time in seconds: %w", EnvProgramTime, t, err)
		}
		g.Time = secs
		g.Commit = getenv(EnvProgramCommit)
		return g, nil
	}
	if root == "" {
		return g, nil
	}
	out, err := exec.CommandContext(ctx, "git", "-C", root, "log", "-1", "--format=%H %ct").Output()
	if err != nil {
		// Not a git checkout, or git is absent: no stamp.
		return Generation{Commit: getenv(EnvProgramCommit)}, nil
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return Generation{}, fmt.Errorf("plunge: git log in %s answered %q, not a commit and a time", root, out)
	}
	secs, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return Generation{}, fmt.Errorf("plunge: git log in %s answered a commit time %q: %w", root, fields[1], err)
	}
	g.Commit, g.Time = fields[0], secs
	if c := getenv(EnvProgramCommit); c != "" {
		g.Commit = c
	}
	return g, nil
}

// deleteGuard is the deploy.Target.DeleteGuard for a run stamped run, over the generations read from the records.
func deleteGuard(run Generation, generations map[resource.URN]Generation) func(*pkgresource.State) string {
	return func(res *pkgresource.State) string {
		return deleteRefusal(run, generations[res.URN])
	}
}

// deleteRefusal is why a run stamped run may not delete a record stamped rec, or "" when it may.
func deleteRefusal(run, rec Generation) string {
	if !rec.newerThan(run) {
		return ""
	}
	return fmt.Sprintf("its record was last written by program %s, newer than this run's %s; a run of an older "+
		"program version does not delete what a newer one wrote (run from the newer commit, or set %s, to delete it)",
		rec, run, EnvProgramTime)
}
