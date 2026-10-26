# Two runs at once on the plunge backend

What happens when two runs write one stack's state at the same time on the
plunge record backend (`plunge+file://`, `plunge+s3://`, `plunge+azblob://`,
`plunge+gs://`, `plunge+k8s://`). The backend is `pkg/backend/plunge`; the record store under it
is choudoufu's `staterecord`, vendored at `pkg/backend/plunge/staterecord`.

## No stack lock

An update, preview, refresh, import or destroy on the plunge backend takes no
stack lock. Each piece of state is its own record, and every record write is
one conditional request, decided by the store in one atomic step:

| Write | Condition |
|---|---|
| Create a record | `If-None-Match: *` (S3, Azure Blob), a does-not-exist precondition (GCS), a create that fails on an existing Secret (Kubernetes), a hard link that fails on an existing name (local) |
| Update a record | the version this run read: `If-Match` (S3, Azure Blob), `ifGenerationMatch` (GCS), the Secret's `resourceVersion` (Kubernetes), a compare under a one-write lock file (local) |
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

`plunge+k8s://<namespace>[/<prefix>]` keeps each record in a Secret in that
namespace, labelled with the estate (the `estate` query key, else the
namespace) that choudoufu's estate-boundary admission policy fences writes
with. The cluster is reached through the kubeconfig (`kubeconfig` and
`context` query keys, else `KUBECONFIG`) or `in_cluster=true`. The DIY backend
has no Kubernetes store, so the stack metadata lives at the DIY URL the
`metadata` query key names, for example
`plunge+k8s://records?metadata=s3://bucket/meta`; a URL without one is refused
by name (`MissingMetadataURLError`).

`plunge+azblob://` reads the storage account's settings when it opens the
store: blob versioning on, a lifecycle rule that deletes previous versions, and
no anonymous blob access. It needs the `resource_group` and `subscription_id`
query keys (or `AZURE_RESOURCE_GROUP` and `AZURE_SUBSCRIPTION_ID`) to read
them. A setting that fails, or that could not be read, refuses the open by
name (`ContractRefusedError`, with the fix for each). Naming the setting in
the `allow_insecure` query key or `PLUNGE_ALLOW_INSECURE` proceeds past it; a
lifecycle rule that deletes current records cannot be waived.

## Races

| Race | Outcome |
|---|---|
| Two runs change different resources | Both land. The snapshot assembled from the records passes `VerifyIntegrity` |
| Two runs change the same resource's record | The first write wins. The second is told by name (`RecordConflictError`, naming the URN and both versions) and writes nothing over it |
| An update racing a destroy of one resource | The loser's conditional write finds a different version, or no record, and is told by name |
| Two first runs create the default provider | The provider's ID is derived from its URN and configuration, so both write the same record; the second adopts the first's |
| A run of an older program version, after a newer one created a resource | The older run keeps the resource, with a warning naming it and both program stamps |
| A run changes a resource that depends on one another run has an operation in flight on | The run is refused before the provider is called (`InFlightDependencyError`, naming both resources and the record). Nothing is written for the dependent, so it holds no value from the other run's unfinished state |

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

## Pending operations

A run marks each operation it begins on a custom resource as a pending
operation in that resource's record, and removes it when the operation ends.
A run that crashes leaves its pending operations where they are. They stay on
the URN until a run acts on that resource: another run that does not touch it
carries the record forward unchanged, since it cannot tell a crashed run's
marker from one still in flight. Until it is cleared, a run that would change
a resource depending on it is refused (`InFlightDependencyError`).

To clear one:

- a pending create: `plunge refresh --clear-pending-creates`, or
  `--import-pending-creates <urn> <id>` when the object was created;
- any pending operation: `plunge stack export --file state.json`, remove the
  entry from `pending_operations`, and `plunge stack import --file state.json`.
  An import is the stack's whole state as given, pending operations included,
  so run it only when no other run is in flight on the stack.

## Stack history and backups

The plunge backend keeps no stack history and no checkpoint backups. A run
writes the records whose content changed, and two runs may be writing one
stack's records at once, so there is no single update for a history entry to
describe. `plunge stack history`, `plunge stack export --version` and
`plunge stack rm --remove-backups` on a plunge stack are refused by name
(`UnsupportedOnRecordsError`). `plunge stack export` gives the current state; a
record store with object versioning keeps each record's earlier versions.
`plunge stack ls` counts each stack's resources from its records.

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
