---
title: Names
description: plunge names an unnamed resource the same way on every run, so two creates of it collide at the cloud instead of making two objects.
prompt: |
  Read https://intentius.io/plunge/how-it-works/names/ and list the resources in this program whose names plunge would choose, and any pulumi:autonaming pattern it would refuse. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Stock Pulumi's `<name>-<random>` lets two concurrent creates make two
objects. plunge uses `<name>-<hash>`, where the hash is
seven hex digits of a SHA-256 over the project, stack, qualified type and name.
It is stable across runs and machines and differs between stacks in one
account.

In the recorded `aws-ts` run, queue `events` became `events-76012a7` and
topic `docs-notify` became `docs-notify-313a6f6`
([Claims and runs](../../claims/)). In the stock-parity baseline these names
are the only preview difference on the AWS and Azure corpus programs.

## C1: two creates of one resource

The cloud refuses a second create of one name, and the loser's next run
binds the winner's object by its markers. This claim, C1
in [CONCURRENCY.md](https://github.com/INTENTIUS/plunge/blob/main/docs/plunge/CONCURRENCY.md),
has a unit test (`TestRacingCreateLoserBindsTheWinnerOnReRun`), and its floci
race waits on [#10](https://github.com/INTENTIUS/plunge/issues/10).

A type is covered when its schema shows the name is unique, it carries
markers, and a lister finds it (or its import ID is its name alone):

| Family | Name evidence |
|---|---|
| pulumi-aws, pulumi-azure, pulumi-gcp | the field the bridge marks autonamed, whose Terraform argument forces a new resource, and which choudoufu's `pkg/identity` builds the type's identity from |
| aws-native | the `autoNamingSpec` property is in the type's `primaryIdentifier` (206 types at v1.83.1) |

Kubernetes and azure-native types keep upstream's naming; plunge reads no
autoname schema for them.

## The program's name comes first

A name the program sets, or a `pulumi:autonaming` pattern without a random
part, is used as given. On a covered type a random part (`${hex(n)}`,
`${uuid}` and the like) is refused (`RandomAutonamePatternError`);
`PLUNGE_ALLOW_RANDOM_AUTONAMES=true` keeps it, outside C1.

## Replacing a named resource

Old and new objects share the name, so plunge deletes first and warns once
per resource where the provider would have created first. The maintainer kept
this on [#12](https://github.com/INTENTIUS/plunge/issues/12), accepting a
window with neither object.

## Where the name came from

Each resource record gives its name's source and whether C1 covers it, or
why not (`ownership.autoname`: `by`, `name`, `c1`, `uncovered`).
CONCURRENCY.md lists every `uncovered` reason.

Source: `pkg/backend/plunge/autoname.go`.
