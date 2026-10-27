---
title: CI/CD
description: Run plunge from a pipeline, with a saved plan checked against the records it read.
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

The saved plan holds the version of each record it read. If any has moved,
`plunge up --plan` is refused by name with exit 40 before anything changes;
plan again ([Resource-level concurrency](../../how-it-works/concurrency/#saved-plans)).

## Credentials

The job needs read and write on the record prefix and read on the bucket's
settings. `plunge+azblob://` takes its key, SAS token or client secret from the
environment only.

## Program stamps

A run stamps each record with the program's git commit and commit time; a
shallow clone works. Without a git checkout, set `PLUNGE_PROGRAM_COMMIT` and
`PLUNGE_PROGRAM_TIME`. An unstamped run deletes only unstamped records, and
keeps resources a stamped run created, with a warning.

## Parallel jobs

Jobs that change different resources of one stack run together, with no
stack lock. Two jobs that touch the same record are refused by name
([Resource-level concurrency](../../how-it-works/concurrency/)).
