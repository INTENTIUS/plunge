# Two runs at once on the plunge backend

What happens when two runs write one stack's state at the same time on the
plunge record backend (`plunge+file://`, `plunge+s3://`, `plunge+azblob://`,
`plunge+gs://`). The backend is `pkg/backend/plunge`; the record store under it
is choudoufu's `staterecord`, vendored at `pkg/backend/plunge/staterecord`.

## No stack lock

An update, preview, refresh, import or destroy on the plunge backend takes no
stack lock. Each piece of state is its own record, and every record write is
one conditional request, decided by the store in one atomic step:

| Write | Condition |
|---|---|
| Create a record | `If-None-Match: *` (S3, Azure Blob), a does-not-exist precondition (GCS), `O_EXCL` (local) |
| Update a record | the version this run read: `If-Match` (S3, Azure Blob), `ifGenerationMatch` (GCS), a compare under a one-write lock file (local) |
| Delete a record | the same condition on the delete |

The local store is for one host: its conditional writes hold a lock file for
the length of one file write, not across a run.

## What a record is

| Key under `stacks/<project>/<stack>/` | Holds |
|---|---|
| `resources/<urn>` | every state for one URN (the live one and any pending delete), and the pending operations on it |
| `outputs/<name>` | one root Stack output |
| `secrets` | the secrets configuration, created once and never rewritten |
| `snippets/<uuid>`, `extensions/<ref>` | one PCL snippet, one extension blob |

A run writes only the records whose content changed since it read them. A
secret re-encrypted to a new ciphertext is compared by its plaintext, so it is
not a change. Two runs changing different resources of one stack both land:
neither writes the other's records.

## Races

| Race | Outcome |
|---|---|
| Two runs change different resources | Both land. The snapshot assembled from the records passes `VerifyIntegrity` |
| Two runs change the same resource's record | The first write wins. The second is told by name (`RecordConflictError`, naming the URN and both versions) and writes nothing over it |
| An update racing a destroy of one resource | The loser's conditional write finds a different version, or no record, and is told by name |
| Two first runs create the default provider | The provider's ID is derived from its URN and configuration, so both write the same record; the second adopts the first's |
| A run of an older program version, after a newer one created a resource | The older run keeps the resource, with a warning naming it and both program stamps |

Every record carries the program stamp (commit and commit time, from git or
from `PLUNGE_PROGRAM_COMMIT` and `PLUNGE_PROGRAM_TIME`) of the run that last
wrote it. A run deletes a resource it no longer declares only when that stamp
is not newer than its own.

## C1: two creates of one client-named resource

Two runs creating the same resource of a covered type ask the cloud for the
same name. The cloud's uniqueness constraint refuses the second create, and
the loser's next run binds the winner's object by its ownership markers
(`plunge-stack`, `plunge-urn`) and creates nothing. When the winner's record
has landed, the next run plans against that record instead.

plunge gets there by naming the resource itself. In place of upstream's random
suffix, the provider's Check is proposed `<name>-<hash>`, where `<name>` is the
URN's name and `<hash>` is seven hex digits of a SHA-256 over the project, the
stack, the URN's qualified type and the name. The same inputs give the same
name on every run and every machine, and two stacks sharing an account get
different names. Where the schema gives a maximum length, the name part is cut
to fit (leaving room for a suffix the provider appends, such as `.fifo`) and
the hash is kept. A replacement of such a resource deletes the old object
before creating the new one, since both would need the same name; the run
warns once per replaced resource when the provider alone would have created
the replacement first.

A type is covered when all of these hold:

| Family | Name evidence |
|---|---|
| pulumi-aws, pulumi-azure, pulumi-gcp (bridged) | the field the bridge's `GetMapping("terraform")` marks autonamed, whose Terraform argument forces a new resource, and which choudoufu's `pkg/identity` builds the type's import identity from (or names as its `UniqueName`) |
| aws-native | metadata.json's `autoNamingSpec` property is in the type's `primaryIdentifier` (206 types at v1.83.1); `maxLength` is read from the same spec |

and the type carries ownership markers (a map or key/value-array tags
property, not classed markerless by `pkg/identity`), and its family's lister
lists it consistently (see the bind phase in `pkg/backend/plunge/bind.go`), so
the loser can find the winner. The provider has to support autonaming
configuration; the engine warns about one that does not.

What the program and the user say comes first. A name the program sets is used
as given. A `pulumi:autonaming` config is used as given when it has no random
part. One with a random part (`${hex(n)}`, `${alphanum(n)}`, `${string(n)}`,
`${num(n)}`, `${uuid}`) on a covered type is refused by name
(`RandomAutonamePatternError`); `PLUNGE_ALLOW_RANDOM_AUTONAMES=true` keeps it,
and those resources are then outside C1.

Every resource record says where its name came from and whether C1 covers it,
with the reason when it does not (`ownership.autoname`: `by`, `name`, `c1`,
`uncovered`). Types C1 does not cover keep upstream's autonaming; where their
family lister covers them, a duplicate create is caught by the marker listing
instead.

## Saved plans

`plunge preview --save-plan` stamps the plan with the version of each record
it was computed against, and `plunge up --plan` checks the stamp before the
update runs. When any of those records has moved since, or the plan carries no
stamp, the update is refused by name (`PlanMovedError`, `UnstampedPlanError`)
and plunge exits 40.

Stock Pulumi uses 3 for an authentication error, and its exit codes grow one
at a time from 0, so plunge's code sits well above them and below the sysexits
range (64 to 78). choudoufu and chant give a moved plan 3, so a driver that
reads plunge's exit status maps 40 to its own moved-plan code. A test in
`pkg/backend/plunge` reads `pkg/cmd/pulumi/cmd/exit_codes.go` and fails if an
upstream merge adds a code equal to 40.

## Reading a stack

The snapshot is assembled from the records on every read, in an order where
each resource comes after its parent, its provider and everything it depends
on. A resource that refers to a parent, a dependency or a provider no record
holds is refused by name (`MissingReferenceError`, naming the resource, the
reference and the record key).

An update and a preview refresh first, so the plan diffs against what the
cloud holds now. `PLUNGE_REFRESH=false` turns that off.

## CI

Serialize applies against one estate in CI anyway, where the real mutex has
always been. Two estates need none
([claim 13](https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/claims/the-tag-is-the-boundary.md)).
