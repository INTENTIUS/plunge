---
title: Deletes
description: A record is never permission to delete. Before any delete, plunge checks the live object's marker, destroy included.
prompt: |
  Read https://intentius.io/plunge/how-it-works/deletes/ and say which resources in this stack a destroy would refuse to delete, and why. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

A record is never read as permission to delete, the same rule as choudoufu's.
Before any delete step exists, every object the run would delete is checked
against its live marker (`pkg/backend/plunge/deletevet.go`). This includes
`plunge destroy`. The check is built on the integration branch with tests
written; neither the tests nor a run have happened yet
([#37](https://github.com/INTENTIUS/plunge/issues/37)).

| The live object | Outcome |
|---|---|
| carries this stack's `plunge-stack` and `plunge-urn` | deleted |
| has a marker naming another stack, or another resource of this stack | the run is refused |
| takes markers but carries none | the run is refused: tags stripped by hand are not consent to delete |
| is of a record-owned type ([Identity](../identity/)) | deleted on the record's word; the record's `ownership` entry says `"by": "record"` |
| is gone | nothing to delete |

The refusal (`DeleteRefusedError`) lists every refused URN with the object ID,
the marker's value and the record key, plus the command that drops the
record: `plunge state delete '<urn>'`. No delete runs.

## Where the marker comes from

With refresh on, the default for `up` and `preview`, the refreshed outputs
hold the tags, so the check makes no extra provider call. With refresh off
(`PLUNGE_REFRESH=false`, or `destroy` without `--refresh`), each marked
object is read once through its provider, under the stack's own provider
configuration.

## When it refuses

In `destroy`, before any step but the refresh. In `up`, deletes are known
only once the program has registered everything, so a run with
`--skip-preview` is refused after its creates and updates and before any
delete. A plain `plunge up` previews first, and the preview runs the same
check, so it is refused before anything changes.

A stack adopted from a stock checkpoint and never updated under plunge has no
markers on its objects yet, so a `destroy` of it is refused for each taggable
object. Run `plunge up` first, which writes the markers, or drop the records
by name. `PLUNGE_MARKERS=false` turns markers off, this check included.

Pending deletes left by a replacement are not checked: they finish replacing
this stack's own resource.
