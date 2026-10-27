---
title: Recover a stack
description: Lost records, a crashed run, a lost machine. What plunge can rebuild today and what it cannot.
prompt: |
  Follow https://intentius.io/plunge/use/recover/ for the failure I describe and tell me which section applies. Change nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

## A run crashed

Re-run it. The crashed run's pending operations stay on the resources it
touched and block changes to their dependents until cleared
([Pending operations and crashes](../pending/)).

## A machine is lost

Nothing is lost with it: the records are in the store, not on the machine.
Log in to the same `plunge+` URL elsewhere. The `plunge+file://` store is the
exception, since it lives on that one host.

## A record was overwritten or deleted

With object versioning on (the store refuses to open without it unless waived),
the bucket keeps each record's earlier versions. Restore the version you want
with the cloud's own tools, then run `plunge preview`. There is no
`plunge stack history` to roll back to ([Records](../../how-it-works/records/)).

## The whole store is lost

plunge has no path today that rebuilds records from the live objects'
markers. If a stock checkpoint of the stack still exists, adopt it again
([Migrate a stock stack](../migrate/)). Rebuilding from markers is
[#48](https://github.com/INTENTIUS/plunge/issues/48).
