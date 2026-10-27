---
title: Remove a resource
description: Delete it from the program and update, or drop its record and leave the object running.
prompt: |
  Follow https://intentius.io/plunge/use/remove/. Remove the resource I name from the program and run `plunge preview`. Report the delete it plans.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

## Destroy the object

Delete the resource from the program and run `plunge up`. Since
[#37](https://github.com/INTENTIUS/plunge/issues/37), plunge first checks the
live object's marker: it deletes the object only when the marker names this
stack and this resource, and refuses the run otherwise
([Deletes](../../how-it-works/deletes/)). That check is built and waits for
its run.

A run of an older checkout of the program does not delete a resource a newer
run created; it keeps it and warns, naming both program stamps.

## Keep the object, drop the record

```sh
plunge state delete '<urn>'
```

This is the command a delete refusal names. How `plunge state delete`, the
other `state` subcommands and `--target` write records, and what they leave on
the live object's markers, is not documented or tested yet
([#47](https://github.com/INTENTIUS/plunge/issues/47)). An object left running
with this stack's markers is reported after each update as an
`unrecorded marked object`; remove its `plunge-*` tags yourself if you mean to
keep it outside the stack.
