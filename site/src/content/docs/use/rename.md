---
title: Rename a resource
description: A rename in Pulumi is an alias. plunge's handling of the marker and the record on a rename has not been checked yet.
prompt: |
  Follow https://intentius.io/plunge/use/rename/. Add the alias for the rename I describe and run `plunge preview`. Report the steps it plans for that resource.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Change the resource's name or parent and add the old URN as an alias, so the
engine treats it as the same resource.

```ts
new aws.s3.Bucket("logs-archive", { /* ... */ }, { aliases: [{ name: "logs" }] });
```

plunge keys the record by URN and writes the URN into the `plunge-urn` marker.
No test or run yet shows whether `plunge up` then rewrites the marker in place
and moves `resources/<old urn>` to `resources/<new urn>`, or how a racing run
sees the rename ([#46](https://github.com/INTENTIUS/plunge/issues/46)). Until
then, check the resource in `plunge preview` first: expect a tags update, and
no replace.
