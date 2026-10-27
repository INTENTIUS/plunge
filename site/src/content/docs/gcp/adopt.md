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

Each resource keeps its ID and gets a record. The next `plunge up` adds the
labels ([Migrate a stock stack](/plunge/use/migrate/)).

plunge refuses by name a provider whose `ignoreLabels` or `defaultLabels`
would hide or override the `plunge-` keys, and leaves pulumi-gcp's
`goog-pulumi-provisioned` label alone.

A type with no labels argument, such as a service account, is record-owned.

## Recorded runs

None on Google Cloud. Adoption is unit-tested; its corpus run waits on
[#7](https://github.com/INTENTIUS/plunge/issues/7).
