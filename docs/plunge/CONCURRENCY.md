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
the loser can find the winner, or the type's import ID is its name alone (see
below). The provider has to support autonaming configuration: one that does not
ignores the proposed name, the engine warns, and the record says C1 does not
cover the resource.

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

When no lister covers a covered type, its listing failed, or its eventually
consistent listing (GCP's Cloud Asset Inventory) found nothing, and the type's
import ID is its name alone (one pkg/identity component holding only the
autonamed argument, or an aws-native primary identifier of the name alone), the
loser's re-run reads the object by its name with the provider's Read. The name
is the program's own, or plunge's deterministic name. An object that carries the
resource's markers is bound; one without them is left alone and the create fails
at the cloud as before. The record says what the read found
(`ownership.byName`).

### What C1 leaves open

Each of these keeps upstream's behaviour, and the record's
`ownership.autoname.uncovered` says which applies. `<tf>` is the Terraform type,
`<arg>` the autonamed argument, `<cf>` the CloudFormation type.

| Gap | `uncovered` reason |
|---|---|
| aws-native type whose name is not in its `primaryIdentifier` (`aws-native:sqs:Queue`, whose identifier is `queueUrl`) | `<cf>'s primary identifier (<ids>) does not include its name <prop>` |
| aws-native type with no autonamed property | `<cf> has no autonamed property in the aws-native metadata` |
| Bridged type whose mapping marks no field autonamed | `the provider's mapping marks no property of <tf> as autonamed` |
| Bridged type whose autonamed argument can change in place | `<tf>'s <arg> can change in place, so it is not taken as the type's identity` |
| Bridged type with no pkg/identity row (every azurerm type, and AWS and Google types the table does not list) | `pkg/identity has no row for <tf>, so nothing says its <arg> is unique` |
| Bridged type whose row is server-assigned or built from other arguments, with no `UniqueName` | `pkg/identity's row for <tf> does not build its identity from <arg> or name it unique` |
| Kubernetes (`metadata.name`) and azure-native: no autoname schema is read | `the schema gives no evidence that this type's names are unique` |
| Untaggable type | `the type carries no ownership marker, so the loser of a racing create has nothing to bind by` |
| Type pkg/identity classes markerless | `pkg/identity classes <tf> as markerless, so the loser of a racing create has nothing to bind by` |
| A random `pulumi:autonaming` pattern kept with `PLUNGE_ALLOW_RANDOM_AUTONAMES=true` | `the pulumi:autonaming pattern has a random part (<exprs>), kept because PLUNGE_ALLOW_RANDOM_AUTONAMES=true` |
| A provider without autonaming-configuration support | `the <pkg>@<version> provider does not support autonaming configuration, so it ignores the proposed name <name> and picks its own` |

Three limits apply inside covered types, and no record reason marks them:

- A bridged type's maximum name length is not in `GetMapping` (the bridge does
  not marshal `AutoNameOptions`), so plunge proposes the full name. A name over
  the maximum is refused by the provider's Check, naming the resource, where
  upstream would have cut its random name. Set the name in the program, or a
  shorter `pulumi:autonaming` pattern without a random part.
- The loser of a racing create is not retried inline. Its run fails with the
  cloud's already-exists error and the re-run binds the winner.
- A covered type that no lister covers and whose import ID is more than its
  name (a pulumi-aws multi-part identifier that `ccid.go` cannot translate,
  see C2 below) has no by-name read: its record carries `ownership.unlisted`,
  and a winner that stopped before its record write is refused by the cloud on
  every re-run until it is imported by hand. The by-name read also skips a name set by a
  `pulumi:autonaming` pattern, since the binder does not evaluate patterns.

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

## C2: the listings behind the collision check

Before a create, the bind phase lists the type's live objects carrying the
resource's markers. One object is bound; two or more are refused by name
(`CollisionError`, naming every ID). The refusal proves there is no duplicate
only when the listing sees a create the moment it returns, so each family lists
through the service's own store:

| Family | Listing |
|---|---|
| pulumi-aws, aws-native | Cloud Control `ListResources` on the CloudFormation type. A pulumi-aws type with a multi-part identifier is covered when choudoufu's `pkg/identity` row spells its Terraform id from exactly those parts, and the identifier is translated to it (`pkg/backend/plunge/ccid.go`) |
| pulumi-azure, azure-native | ARM's own list, filtered by the `plunge-urn` tag |
| pulumi-kubernetes | a LIST of the kind by label selector |
| pulumi-gcp | the type's own service list (`pkg/backend/plunge/gcplist`): the parent collection of the Asset name choudoufu's `pkg/identity` gives the type, `projects/<project>/topics` for a topic. 143 types across 28 services at choudoufu's pkg/identity pin, plus ten server-assigned types written out from choudoufu's gcpsweep (Vertex AI, Document AI, Transcoder, Service Directory, reCAPTCHA Enterprise, Managed Microsoft AD): 153 in all, listed by `gcplist.Covered()` and the same set choudoufu lists (`gcplist/testdata/covered-types.txt`) |

A pulumi-gcp type outside `gcplist.Covered()`, or one whose list path cannot be
filled from the resource's inputs and the provider's project, region and zone,
is searched through Cloud Asset Inventory. That index lags its services, so it
binds and does not back the collision check.

Each resource record says which listing ran when it was created
(`ownership.listing`, for example `gcp: pubsub.googleapis.com list of
projects/plc/topics (consistent per type)`), and `ownership.unlisted` holds the
reason when the collision check does not cover it.

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

## Design choices

The maintainer reviewed these five on INTENTIUS/plunge#12 (2026-10-25) and
kept each as built.

| Choice | What plunge does | Cost accepted |
|---|---|---|
| Replacing a deterministically autonamed resource | Deletes the old object before creating the new one, warning once per URN when the provider alone would have created first | A window in which neither object exists |
| A new provider resource's ID | A v5 UUID of the URN and the provider's configuration, with secret values hashed as a fixed placeholder, so two first runs converge on one default provider | A forced replace of a provider with identical configuration gets the old ID again |
| Which run may delete a resource the program no longer registers | The run whose program commit time is not older than the stamp on the resource's record (`PLUNGE_PROGRAM_TIME`, else `git log -1 --format=%ct`) | Committer clock skew can misorder two commits, and equal times allow the delete |
| Refresh before update and preview | On by default on the plunge backend; `PLUNGE_REFRESH=false` turns it off | One provider Read per resource on every run |
| Where a saved plan carries its record versions | `plungeRecordVersions` on `apitype.DeploymentPlanV1`, omitted for every other backend | An SDK struct an upstream merge can conflict on |

## CI

Serialize applies against one estate in CI anyway, where the real mutex has
always been. Two estates need none
([claim 13](https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/claims/the-tag-is-the-boundary.md)).
