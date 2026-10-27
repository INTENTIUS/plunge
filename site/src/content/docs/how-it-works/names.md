---
title: Names
description: plunge names an unnamed resource the same way on every run, so two creates of it collide at the cloud instead of making two objects.
prompt: |
  Read https://intentius.io/plunge/how-it-works/names/ and list the resources in this program whose names plunge would choose, and any pulumi:autonaming pattern it would refuse. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Stock Pulumi names an unnamed resource `<name>-<random>`. Two runs creating
it at once make two objects. plunge proposes `<name>-<hash>` instead, where
`<hash>` is seven hex digits of a SHA-256 over the project, the stack, the
URN's qualified type and the name (`pkg/backend/plunge/autoname.go`). The
same inputs give the same name on every run and every machine, and two stacks
sharing an account get different names.

In the recorded `aws-ts` run the queue `events` became `events-76012a7`
and the topic `docs-notify` became `docs-notify-313a6f6`
([Claims and runs](../../claims/)). The stock-parity baseline agrees: on the
AWS and Azure corpus programs, these names are the only difference between
plunge's preview and stock Pulumi's.

## C1: two creates of one resource

With the same name, the cloud's uniqueness constraint refuses the second
create. The loser's next run finds the winner's object by its markers, binds
it and creates nothing. This is claim C1 in
[CONCURRENCY.md](https://github.com/INTENTIUS/plunge/blob/main/docs/plunge/CONCURRENCY.md).
It is unit-tested (`TestRacingCreateLoserBindsTheWinnerOnReRun`); the race on
floci waits on [#10](https://github.com/INTENTIUS/plunge/issues/10).

A type is covered when the schema gives evidence its name is unique, it
carries markers, and a lister finds it (or its import ID is its name alone):

| Family | Name evidence |
|---|---|
| pulumi-aws, pulumi-azure, pulumi-gcp | the field the bridge marks autonamed, whose Terraform argument forces a new resource, and which choudoufu's `pkg/identity` builds the type's identity from |
| aws-native | the `autoNamingSpec` property is in the type's `primaryIdentifier` (206 types at v1.83.1) |

Kubernetes and azure-native types keep upstream's naming: no autoname schema
is read for them.

## What the program says comes first

A name the program sets is used as given. A `pulumi:autonaming` pattern
with no random part is used as given. One with a random part (`${hex(n)}`,
`${uuid}` and the like) on a covered type is refused
(`RandomAutonamePatternError`); `PLUNGE_ALLOW_RANDOM_AUTONAMES=true` keeps
it, and those resources fall outside C1.

## Replacing a named resource

The old and the new object would need the same name, so plunge deletes the
old one before creating the new one, and warns once per resource when the
provider alone would have created first. The maintainer kept this on
[#12](https://github.com/INTENTIUS/plunge/issues/12), accepting a window in
which neither object exists.

## Where the name came from

Each resource record says where its name came from and whether C1 covers it,
with the reason when it does not (`ownership.autoname`: `by`, `name`,
`c1`, `uncovered`). CONCURRENCY.md lists every `uncovered` reason.
