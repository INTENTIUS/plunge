---
title: Deletes
description: A record is never permission to delete. plunge checks the live marker before every delete, destroy included.
prompt: |
  Read https://intentius.io/plunge/how-it-works/deletes/ and say which resources in this stack a destroy would refuse to delete, and why. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

As in choudoufu, a record is never permission to delete. Every object a run
would delete, under `plunge destroy` too, is first checked against its live
marker (`pkg/backend/plunge/deletevet.go`). The check and its tests are on the
integration branch; neither the tests nor a run have happened yet
([#37](https://github.com/INTENTIUS/plunge/issues/37)).

| The live object | Outcome |
|---|---|
| carries this stack's `plunge-stack` and `plunge-urn` | deleted |
| has a marker naming another stack, or another resource of this stack | the run is refused |
| takes markers but carries none | the run is refused: tags stripped by hand are not consent to delete |
| is of a record-owned type ([Identity](../identity/)) | deleted on the record's word; the record's `ownership` entry says `"by": "record"` |
| is gone | nothing to delete |

The refusal (`DeleteRefusedError`) lists each URN with its object ID, marker
value, record key and `plunge state delete '<urn>'`. No delete runs.

## Where the marker comes from

With refresh on (the default for `up` and `preview`), the refreshed outputs
hold the tags, so no extra provider call is made. With refresh off
(`PLUNGE_REFRESH=false`, or `destroy` without `--refresh`), each marked object
is read once under the stack's provider configuration.

## When it refuses

`destroy` refuses before any step but the refresh. `up` knows its deletes
only after the program registers everything, so `--skip-preview` is refused
after creates and updates, before any delete. A plain `plunge up` is refused in
its preview, before anything changes.

An adopted stack never updated under plunge has no markers yet, so its
`destroy` is refused for each taggable object. Run `plunge up` to write them,
or drop the records by name. `PLUNGE_MARKERS=false` turns off markers and this
check.

Pending deletes left by a replacement are not checked: they finish replacing
this stack's own resource.
