---
title: CI/CD
description: Run plunge from a pipeline. Serialize updates to one stack anyway.
prompt: |
  Follow https://intentius.io/plunge/use/cicd/ and draft a pipeline for this repository: a preview job that saves a plan on pull requests, and an update job that applies it after approval. Show me the file; do not commit it.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

## Preview, review, update

```sh
plunge preview --stack prod --save-plan plan.json
# review plan.json, then in the approved job:
plunge up --stack prod --plan plan.json --yes
```

The saved plan carries the version of each record it read. If any has moved
since, `plunge up --plan` is refused by name and exits 40, before anything
changes. Treat 40 as "plan again" ([Resource-level concurrency](../../how-it-works/concurrency/#saved-plans)).

## Credentials

Give the job read and write on the record prefix and read on the bucket's
settings. Give `plunge+azblob://` its key, SAS token or client secret in the
environment, never in the URL.

## Program stamps

A run stamps each record with the program's commit and commit time from git.
A shallow clone works, since the commit time is in the record itself. A run
with no stamp (no git checkout and no `PLUNGE_PROGRAM_COMMIT` and
`PLUNGE_PROGRAM_TIME`) deletes only records that carry no stamp either, so it
keeps resources a stamped run created, with a warning. Set both variables
when the job has no git checkout.

## One update at a time

Serialize updates against one stack in CI anyway, where the real mutex has
always been. Two stacks need none.
