---
title: Pending operations and crashes
description: The pending operations a crashed run leaves on a resource's record, the runs they block, and how to clear them.
prompt: |
  Follow https://intentius.io/plunge/use/pending/. Run `plunge stack export` for this stack and list every pending operation with its URN. Change nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

A run records each operation on a custom resource as pending in its record
until the operation ends. A crash leaves them in place.

They stay until a run acts on that resource; other runs carry the record
forward, unable to tell a crashed operation from one in flight. Until cleared,
a run changing a dependent is refused (`InFlightDependencyError`, naming
both).

No stack lock needs releasing; `pulumi cancel` and lock files play no part.

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

An import writes the whole stack state as given; run it only with no other
run in flight on the stack.
