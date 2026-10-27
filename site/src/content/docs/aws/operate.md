---
title: Operate
description: Rename, remove, apply a saved plan, run from CI, and run twice at once on AWS.
prompt: |
  Follow https://intentius.io/plunge/aws/operate/ for my change. Edit the program, run `plunge preview --save-plan plan.json` and report the plan.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Rename

A Pulumi rename is an `aliases` entry. Its effect on the `plunge-urn` tag and
the record key is open on [#46](https://github.com/INTENTIUS/plunge/issues/46)
([Rename a resource](/plunge/use/rename/)).

## Remove

Delete it from the program and update; plunge checks the live tags first
([#37](https://github.com/INTENTIUS/plunge/issues/37), tested).
`plunge state delete '<urn>'` drops the record and keeps the object
([#47](https://github.com/INTENTIUS/plunge/issues/47), [Remove a resource](/plunge/use/remove/)).

## Plan, review, update

Run `plunge preview --save-plan plan.json`, then `plunge up --plan plan.json`,
which is refused by name, exit 40, if any record the plan read has moved.

## From CI

[CI/CD](/plunge/use/cicd/) covers pipelines.

## Two runs at once

There is no stack lock. Creates carry their tags and Cloud Control lists a
type's marked objects before each create, so each run sees the other's. An
unnamed resource gets one name in both, so the second create fails at AWS and
its re-run binds the winner ([Names](/plunge/how-it-works/names/)).

## Endpoints

Cloud Control listings follow the provider: `PLUNGE_CLOUDCONTROL_ENDPOINT`,
else `aws:endpoints`' `cloudcontrol` entry, then
`AWS_ENDPOINT_URL_CLOUDCONTROL` or `AWS_ENDPOINT_URL`, then the URL every entry
shares. If no custom endpoint suits Cloud Control, plunge skips the listing
with a warning and never calls AWS's public endpoint
([#27](https://github.com/INTENTIUS/plunge/issues/27)).
