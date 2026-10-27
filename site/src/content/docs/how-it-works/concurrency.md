---
title: Resource-level concurrency
description: Pulumi locks the stack. plunge takes no lock; record writes are conditional and a clash is refused by name.
prompt: |
  Read https://intentius.io/plunge/how-it-works/concurrency/ and say how our CI serializes updates to one stack. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Stock Pulumi locks the stack for every update, so updates to different
resources wait for each other. plunge takes no stack lock for update, preview,
refresh, import or destroy; each record write is one atomic conditional
request ([Records](../records/)).

## Two updates, one stack

| | Stock DIY backend | plunge |
|---|---|---|
| Run A changes `db`, run B changes `dns` | B waits for A's lock or fails | Both land; neither writes the other's records |
| Both change `db`'s record | B waits | The first write wins; the second is refused (`RecordConflictError`, naming the URN and both versions) and writes nothing over it |
| An update racing a destroy of one resource | serialized by the lock | The loser's conditional write finds another version, or no record, and is refused by name |
| Two first runs create the default provider | serialized | The provider's ID is a v5 UUID of its URN and configuration, so both write the same record |
| A run changes a resource that depends on one another run has in flight | serialized | Refused before the provider is called (`InFlightDependencyError`, naming both resources) |
| A run killed part way | leaves the stack locked until the lock is removed | leaves pending operations on the resources it touched ([Pending operations](../../use/pending/)) |

Rows one, four and five are unit-tested
(`TestTwoConcurrentUpdatesOnDisjointResourcesBothLand`,
`TestTwoFirstRunsConvergeOnOneDefaultProvider`,
`TestDependentOfAnotherRunsInFlightResourceIsRefused`); floci races wait on
[#6](https://github.com/INTENTIUS/plunge/issues/6) and
[#7](https://github.com/INTENTIUS/plunge/issues/7). The recorded `aws-ts` run's
two providers have v5 UUID IDs.

## Older programs and deletes

Each record carries its last writer's program stamp (commit and commit time,
from git or `PLUNGE_PROGRAM_COMMIT` and `PLUNGE_PROGRAM_TIME`). A run deletes
an undeclared resource only if that stamp is no newer than its own, so an
older checkout keeps a newer one's resource and warns with both stamps. Since [#37](https://github.com/INTENTIUS/plunge/issues/37) the live
marker is checked too ([Deletes](../deletes/)).

## Saved plans

`plunge preview --save-plan` stamps the plan with each record version it
read. `plunge up --plan` refuses a plan whose records moved or that has no
stamp (`PlanMovedError`, `UnstampedPlanError`) and exits 40. choudoufu and
chant use 3 for a moved plan, which stock Pulumi uses for an authentication
error, so a driver maps 40 to its own code
(`TestMovedPlanIsRefusedWithExitCode40`).

## C2: the listing behind the collision check

Before a create, plunge lists live objects with the resource's markers. It
binds one and refuses two or more (`CollisionError`, naming every ID). An
empty listing proves no other create landed only if the listing sees creates
at once, so the claim is per family:

| Family | Listing | Collision claim |
|---|---|---|
| pulumi-aws, aws-native | Cloud Control `ListResources` | claimed |
| pulumi-kubernetes | a LIST by label selector, a quorum read | claimed |
| pulumi-gcp, Cloud Storage | the bucket list | claimed |
| pulumi-gcp, other services | the service's own list (`gcplist`, 156 types) | binds, consistency unmeasured |
| pulumi-azure, azure-native | ARM's list, filtered by the `plunge-urn` tag | binds, consistency unmeasured ([#15](https://github.com/INTENTIUS/plunge/issues/15)) |

"Claimed" rests on the service's documented consistency; no race has
measured it. Listers call the endpoints in the stack's provider configuration,
never a public cloud fallback
([#27](https://github.com/INTENTIUS/plunge/issues/27), unit-tested).

After `up` and `preview`, plunge lists each type the run marked and warns,
without deleting, about live objects with this stack's marker that no record
names (`unrecorded marked object`).

## In CI

Serialize updates to one stack in CI anyway, where the real mutex has
always been. Two stacks need none.
