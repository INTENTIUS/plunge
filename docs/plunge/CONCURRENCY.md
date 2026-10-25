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
