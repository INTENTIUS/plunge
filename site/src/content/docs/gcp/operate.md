---
title: Operate
description: Rename, remove, apply a saved plan, and run twice at once on Google Cloud.
prompt: |
  Follow https://intentius.io/plunge/gcp/operate/ for my change. Edit the program, run `plunge preview --save-plan plan.json` and report the plan.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Rename

A Pulumi rename is an `aliases` entry; plunge's handling of the label and the
record key on a rename is open on [#46](https://github.com/INTENTIUS/plunge/issues/46).

## Remove

Delete it from the program and update; plunge checks the live labels first ([#37](https://github.com/INTENTIUS/plunge/issues/37), tested).

## Plan, review, update

`plunge preview --save-plan plan.json`, then `plunge up --plan plan.json`.
A moved record refuses it by name, exit 40.

## Two runs at once

There is no stack lock. Before a create, plunge binds a marked object found
in the type's service list. Only the Cloud Storage bucket list backs the
collision claim. The other service lists bind with unmeasured consistency; for
unlisted types the fallback is Cloud Asset Inventory, which lags and only binds.

## Endpoints and tokens

A list uses the service's `<service>CustomEndpoint` input or
`GOOGLE_*_CUSTOM_ENDPOINT`; custom endpoints for other services only skip it
with a warning ([#27](https://github.com/INTENTIUS/plunge/issues/27)). With all named endpoints local, the listers
send no credential (or only a static `accessToken`) and fetch no Google token
([#42](https://github.com/INTENTIUS/plunge/issues/42), tested).
