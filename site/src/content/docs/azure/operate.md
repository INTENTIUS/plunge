---
title: Operate
description: Rename, remove, apply a saved plan, and run twice at once on Azure.
prompt: |
  Follow https://intentius.io/plunge/azure/operate/ for my change. Edit the program, run `plunge preview --save-plan plan.json` and report the plan.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Rename

A Pulumi rename is an `aliases` entry; plunge's handling of the tag and the
record key on a rename is open on [#46](https://github.com/INTENTIUS/plunge/issues/46).

## Remove

Delete it from the program and update; plunge checks the live tags first ([#37](https://github.com/INTENTIUS/plunge/issues/37), built, waiting for its run).

## Plan, review, update

`plunge preview --save-plan plan.json`, then `plunge up --plan plan.json`.
A moved record refuses it by name, exit 40.

## Two runs at once

There is no stack lock. Before a create, plunge binds a marked object that
ARM lists by `plunge-urn`. ARM's list consistency is unmeasured, so the
collision claim excludes it ([#15](https://github.com/INTENTIUS/plunge/issues/15)).

## Emulators

With `metadataHost` (or `ARM_METADATA_HOSTNAME`), the ARM lister lists through
the ARM and login endpoints in that host's `/metadata/endpoints`, taking a
client secret only from the environment ([#41](https://github.com/INTENTIUS/plunge/issues/41), built, tests not run).
Lacking the three `ARM_*` variables, it warns and calls nothing.
