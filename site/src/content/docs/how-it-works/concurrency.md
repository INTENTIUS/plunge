---
title: Resource-level concurrency
description: Pulumi locks the whole stack. plunge takes no lock; each record write is conditional, and a clash is refused by name.
prompt: |
  Read https://intentius.io/plunge/how-it-works/concurrency/ and say how our CI serializes updates to one stack. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Stock Pulumi takes a lock on the stack for every update, so two updates to
different resources of one stack wait for each other. plunge takes no stack
lock for update, preview, refresh, import or destroy. Each record write is one
conditional request that the store decides atomically
([Records](../records/)).

## Two updates, one stack

| | Stock DIY backend | plunge |
|---|---|---|
| Run A changes `db`, run B changes `dns` | B waits for A's lock or fails | Both land; neither writes the other's records |
| Both change `db`'s record | B waits | The first write wins; the second is refused (`RecordConflictError`, naming the URN and both versions) and writes nothing over it |
| An update racing a destroy of one resource | serialized by the lock | The loser's conditional write finds another version, or no record, and is refused by name |
| Two first runs create the default provider | serialized | The provider's ID is a v5 UUID of its URN and configuration, so both write the same record |
| A run changes a resource that depends on one another run has in flight | serialized | Refused before the provider is called (`InFlightDependencyError`, naming both resources) |
| A run killed part way | leaves the stack locked until the lock is removed | leaves pending operations on the resources it touched ([Pending operations](../../use/pending/)) |

The first, fourth and fifth rows are unit-tested
(`TestTwoConcurrentUpdatesOnDisjointResourcesBothLand`,
`TestTwoFirstRunsConvergeOnOneDefaultProvider`,
`TestDependentOfAnotherRunsInFlightResourceIsRefused`); the races on floci
wait on [#6](https://github.com/INTENTIUS/plunge/issues/6) and
[#7](https://github.com/INTENTIUS/plunge/issues/7). The recorded `aws-ts` run's
two providers both have v5 UUID IDs.

## Older programs and deletes

Every record carries the program stamp (commit and commit time, from git or
`PLUNGE_PROGRAM_COMMIT` and `PLUNGE_PROGRAM_TIME`) of the run that last
wrote it. A run deletes a resource it no longer declares only when that stamp
is not newer than its own, so a run of an older checkout keeps a resource a
newer one created, with a warning naming both stamps. Since
[#37](https://github.com/INTENTIUS/plunge/issues/37) the live marker is
checked too ([Deletes](../deletes/)).

## Saved plans

`plunge preview --save-plan` stamps the plan with the version of each record
it read. `plunge up --plan` refuses it when any of those records has moved, or
when the plan has no stamp (`PlanMovedError`, `UnstampedPlanError`), and
exits 40. choudoufu and chant use 3 for a moved plan, but stock Pulumi uses 3
for an authentication error, so a driver maps plunge's 40 to its own code.
Unit-tested (`TestMovedPlanIsRefusedWithExitCode40`).

## C2: the listing behind the collision check

Before a create, plunge lists the type's live objects carrying the resource's
markers. One is bound. Two or more are refused (`CollisionError`, naming every
ID). An empty listing proves no other run's create landed only when the
listing sees a create the moment it returns, so the claim is made per family:

| Family | Listing | Collision claim |
|---|---|---|
| pulumi-aws, aws-native | Cloud Control `ListResources` | claimed |
| pulumi-kubernetes | a LIST by label selector, a quorum read | claimed |
| pulumi-gcp, Cloud Storage | the bucket list | claimed |
| pulumi-gcp, other services | the service's own list (`gcplist`, 156 types) | binds, consistency unmeasured |
| pulumi-azure, azure-native | ARM's list, filtered by the `plunge-urn` tag | binds, consistency unmeasured ([#15](https://github.com/INTENTIUS/plunge/issues/15)) |

"Claimed" is what the code asserts from each service's documented
consistency; no race has measured it yet. Each lister calls the endpoints the
stack's provider configuration names and never falls back to a public cloud
endpoint ([#27](https://github.com/INTENTIUS/plunge/issues/27), unit-tested).

After each `up` and `preview`, plunge also lists every type the run marked
and warns about any live object carrying this stack's marker that no record
names (`unrecorded marked object`). It never deletes one.

## In CI

Serialize updates against one stack in CI anyway, where the real mutex has
always been. Two stacks need none.
