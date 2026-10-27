---
title: Resource-level concurrency
description: Each record write in plunge is conditional, so runs on different resources of one stack proceed together and a clash is refused by name.
prompt: |
  Read https://intentius.io/plunge/how-it-works/concurrency/ and say which of our CI jobs can update one stack together. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Pulumi's stack lock keeps each update to one stack in order. plunge keeps one
record per resource, so updates to different resources can run together. It
takes no stack lock for update, preview, refresh, import or destroy; each
record write is one atomic conditional request ([Records](../records/)).

## Two updates on one plunge stack

The stock DIY backend orders these runs with its stack lock. On plunge:

| Runs | Outcome |
|---|---|
| Run A changes `db`, run B changes `dns` | Both land; neither writes the other's records |
| Both change `db`'s record | The first write wins; the second is refused (`RecordConflictError`, naming the URN and both versions) and writes nothing over it |
| An update racing a destroy of one resource | The loser's conditional write finds another version, or no record, and is refused by name |
| Two first runs create the default provider | The provider's ID is a v5 UUID of its URN and configuration, so both write the same record |
| A run changes a resource that depends on one another run has in flight | Refused before the provider is called (`InFlightDependencyError`, naming both resources) |
| A run killed part way | Leaves pending operations on the resources it touched ([Pending operations](../../use/pending/)) |

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
error, so a driver maps 40 to its own code.

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

"Claimed" rests on the service's documented consistency. Listers call the endpoints in the stack's provider configuration,
never a public cloud fallback
([#27](https://github.com/INTENTIUS/plunge/issues/27)).

After `up` and `preview`, plunge lists each type the run marked and warns,
without deleting, about live objects with this stack's marker that no record
names (`unrecorded marked object`).

## In CI

Jobs that change different resources of one stack run together. Each
saved plan is checked against the records it read ([CI/CD](../../use/cicd/)).
