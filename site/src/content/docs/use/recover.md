---
title: Recover a stack
description: Recovery from lost records, a crashed run or a lost machine, and the cases plunge cannot rebuild today.
prompt: |
  Follow https://intentius.io/plunge/use/recover/ for the failure I describe and tell me which section applies. Change nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

## A run crashed

Re-run it. Its pending operations stay on the resources it touched and block
changes to their dependents until cleared
([Pending operations and crashes](../pending/)).

## A machine is lost

The records live in the store, so log in to the same `plunge+` URL elsewhere.
The exception is `plunge+file://`, which lives on that one host.

## A record was overwritten or deleted

Object versioning, which the store requires unless waived, keeps each
record's earlier versions. Restore one with the cloud's own tools, then run
`plunge preview`; there is no `plunge stack history`
([Records](../../how-it-works/records/)).

## The whole store is lost

plunge cannot yet rebuild records from the live objects' markers
([#48](https://github.com/INTENTIUS/plunge/issues/48)). If a stock checkpoint
of the stack exists, adopt it again ([Migrate a stock stack](../migrate/)).
