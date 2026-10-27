---
title: Operate
description: Rename, remove, apply a saved plan, and run twice at once on Azure.
prompt: |
  Follow https://intentius.io/plunge/azure/operate/ for my change. Edit the program, run `plunge preview --save-plan plan.json` and report the plan.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Rename

A Pulumi rename is an `aliases` entry; what plunge does with the tag and the
record key is open on [#46](https://github.com/INTENTIUS/plunge/issues/46).

## Remove

Delete the resource from the program and update. plunge checks the live tags
before it deletes ([#37](https://github.com/INTENTIUS/plunge/issues/37), built, waiting for its run).

## Plan, review, update

`plunge preview --save-plan plan.json`, then `plunge up --plan plan.json`.
A moved record refuses the update by name, exit 40.

## Two runs at once

There is no stack lock. Before a create, plunge lists the type through ARM,
filtered by the `plunge-urn` tag, and binds a marked object it finds. ARM's
list consistency is unmeasured, so this binds and does not back the collision
claim ([#15](https://github.com/INTENTIUS/plunge/issues/15)).

## Emulators

With the azure provider's `metadataHost` (or `ARM_METADATA_HOSTNAME`), the ARM
lister reads that host's `/metadata/endpoints` and lists through the ARM and
login endpoints it names, with a client secret from the environment only
([#41](https://github.com/INTENTIUS/plunge/issues/41), built, tests written and not run). Without the three `ARM_*`
variables it skips the listing with a warning and calls nothing.
