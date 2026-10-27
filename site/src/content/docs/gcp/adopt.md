---
title: Adopt
description: Move a stock Pulumi stack on Google Cloud onto plunge records.
prompt: |
  Follow https://intentius.io/plunge/gcp/adopt/. Run `plunge stack adopt --json --stack <stack>` without --yes and report the status and every refusal.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
```
plunge stack adopt --json --stack dev        # classify, write nothing
plunge stack adopt --json --yes --stack dev  # adopt
```

Every resource keeps its ID and gets its own record. The next `plunge up`
adds both labels in place ([Migrate a stock stack](/plunge/use/migrate/)).

A provider whose `ignoreLabels` or `defaultLabels` would hide or override the
`plunge-` keys is refused by name. pulumi-gcp's own `goog-pulumi-provisioned`
label is left alone.

A type with no labels argument, such as a service account, is record-owned.

## What has run

Nothing on Google Cloud. Adoption is unit-tested, and the corpus run waits on
[#7](https://github.com/INTENTIUS/plunge/issues/7).
