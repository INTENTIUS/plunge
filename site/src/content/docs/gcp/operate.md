---
title: Operate
description: Rename, remove, apply a saved plan, and run twice at once on Google Cloud.
prompt: |
  Follow https://intentius.io/plunge/gcp/operate/ for my change. Edit the program, run `plunge preview --save-plan plan.json` and report the plan.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Rename

A Pulumi rename is an `aliases` entry; what plunge does with the label and the
record key is open on [#46](https://github.com/INTENTIUS/plunge/issues/46).

## Remove

Delete the resource from the program and update. plunge checks the live
labels before it deletes ([#37](https://github.com/INTENTIUS/plunge/issues/37), built, waiting for its run).

## Plan, review, update

`plunge preview --save-plan plan.json`, then `plunge up --plan plan.json`.
A moved record refuses the update by name, exit 40.

## Two runs at once

There is no stack lock. Before a create, plunge lists the type through its
own service and binds a marked object it finds. Only the Cloud Storage bucket
list backs the collision claim; other services bind with consistency
unmeasured, and a type outside the service lists goes through Cloud Asset
Inventory, which lags and binds only.

## Endpoints and tokens

A Google list uses the service's `<service>CustomEndpoint` input or its
`GOOGLE_*_CUSTOM_ENDPOINT` variable; a stack naming custom endpoints for other
services only skips the listing with a warning ([#27](https://github.com/INTENTIUS/plunge/issues/27)). When every named
endpoint is local, the listers send no credential, or the stack's static
`accessToken`, and fetch no token from Google ([#42](https://github.com/INTENTIUS/plunge/issues/42), built, tests written
and not run).
