---
title: Remove a resource
description: Delete it from the program and update, or drop its record and leave the object running.
prompt: |
  Follow https://intentius.io/plunge/use/remove/. Remove the resource I name from the program and run `plunge preview`. Report the delete it plans.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

## Destroy the object

Delete the resource from the program and run `plunge up`. Since
[#37](https://github.com/INTENTIUS/plunge/issues/37), plunge deletes the object
only when its live marker names this stack and resource, and refuses the run
otherwise ([Deletes](../../how-it-works/deletes/)).

A run of an older program checkout keeps a resource a newer run created and
warns, naming both stamps.

## Keep the object, drop the record

```sh
plunge state delete '<urn>'
```

A delete refusal names this command. How it and the other `state` subcommands
and `--target` change records and live markers is untested and undocumented
([#47](https://github.com/INTENTIUS/plunge/issues/47)). Each
update reports a running object with this stack's markers as an
`unrecorded marked object`; remove its `plunge-*` tags to keep it outside the
stack.
