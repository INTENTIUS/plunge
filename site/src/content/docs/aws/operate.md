---
title: Operate
description: Rename, remove, apply a saved plan, run from CI, and run twice at once on AWS.
prompt: |
  Follow https://intentius.io/plunge/aws/operate/ for my change. Edit the program, run `plunge preview --save-plan plan.json` and report the plan.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Rename

A Pulumi rename is an `aliases` entry. What plunge does with the
`plunge-urn` tag and the record key is open on [#46](https://github.com/INTENTIUS/plunge/issues/46)
([Rename a resource](/plunge/use/rename/)).

## Remove

Delete the resource from the program and update. plunge checks the live
object's tags before it deletes ([#37](https://github.com/INTENTIUS/plunge/issues/37), built, waiting for its run).
`plunge state delete '<urn>'` drops a record and leaves the object
([#47](https://github.com/INTENTIUS/plunge/issues/47)). See [Remove a resource](/plunge/use/remove/).

## Plan, review, update

`plunge preview --save-plan plan.json`, then `plunge up --plan plan.json`.
If any record the plan read has moved, the update is refused by name and
exits 40.

## From CI

See [CI/CD](/plunge/use/cicd/).

## Two runs at once

There is no stack lock. Each create carries its tags, and Cloud Control lists
a type's marked objects before a create, so a run sees the other run's
objects. An unnamed resource gets the same name in both runs, so the second
create fails at AWS and the re-run binds the winner
([Names](/plunge/how-it-works/names/)). Unit-tested; the races on floci wait
on [#7](https://github.com/INTENTIUS/plunge/issues/7), [#10](https://github.com/INTENTIUS/plunge/issues/10) and [#36](https://github.com/INTENTIUS/plunge/issues/36).

## Endpoints

Cloud Control listings go where the provider goes: `PLUNGE_CLOUDCONTROL_ENDPOINT`
if set, else the `cloudcontrol` entry of `aws:endpoints`, then
`AWS_ENDPOINT_URL_CLOUDCONTROL` or `AWS_ENDPOINT_URL`, then the one URL every
entry shares. A configuration naming custom endpoints none of which Cloud
Control can use skips the listing with a warning and never calls AWS's public
endpoint ([#27](https://github.com/INTENTIUS/plunge/issues/27)).
