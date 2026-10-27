---
title: Pending operations and crashes
description: What a crashed run leaves on a resource's record, what it blocks, and how to clear it.
prompt: |
  Follow https://intentius.io/plunge/use/pending/. Run `plunge stack export` for this stack and list every pending operation with its URN. Change nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

A run marks each operation it begins on a custom resource as a pending
operation in that resource's record, and removes it when the operation ends.
A run that crashes leaves its pending operations where they are.

They stay on the URN until a run acts on that resource. Another run that does
not touch it carries the record forward unchanged, since it cannot tell a
crashed run's operation from one still in flight. Until it is cleared, a run
that would change a resource depending on it is refused
(`InFlightDependencyError`, naming both resources).

There is no stack lock to release: `pulumi cancel` and lock files have no
part in it.

## Clear one

### A pending create

```sh
plunge refresh --clear-pending-creates
plunge refresh --import-pending-creates <urn> <id>   # when the object was created
```

### Any pending operation

```sh
plunge stack export --file state.json
# remove the entry from pending_operations
plunge stack import --file state.json
```

An import writes the stack's whole state as given, so run it only when no
other run is in flight on the stack.

## What has run

The refusal is unit-tested
(`TestDependentOfAnotherRunsInFlightResourceIsRefused`). A crash and re-run
on floci is race C6, which waits on
[#7](https://github.com/INTENTIUS/plunge/issues/7).
