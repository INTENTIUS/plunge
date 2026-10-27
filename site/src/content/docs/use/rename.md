---
title: Rename a resource
description: A rename in Pulumi is an alias. What plunge does with the marker and the record on a rename has not been checked yet.
prompt: |
  Follow https://intentius.io/plunge/use/rename/. Add the alias for the rename I describe and run `plunge preview`. Report the steps it plans for that resource.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Rename a resource the Pulumi way: change its name or parent and add the old
URN as an alias, so the engine treats it as the same resource.

```ts
new aws.s3.Bucket("logs-archive", { /* ... */ }, { aliases: [{ name: "logs" }] });
```

plunge carries the URN in the `plunge-urn` marker and keys the record by it.
Whether the next `plunge up` rewrites the marker in place, moves the record
from `resources/<old urn>` to `resources/<new urn>`, and what a run racing the
rename sees, has not been tested or run. [#46](https://github.com/INTENTIUS/plunge/issues/46)
holds it. Until it is answered, run `plunge preview` and read the plan for the
renamed resource before you update: a tags update on it is expected, and a
replace is not.
