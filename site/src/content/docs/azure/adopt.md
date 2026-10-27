---
title: Adopt
description: Move a stock Pulumi stack on Azure onto plunge records.
prompt: |
  Follow https://intentius.io/plunge/azure/adopt/. Run `plunge stack adopt --json --stack <stack>` without --yes and report the status and every refusal.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
```
plunge stack adopt --json --stack dev        # classify, write nothing
plunge stack adopt --json --yes --stack dev  # adopt
```

Every resource keeps its ID and gets its own record. The next `plunge up`
adds both tags in place ([Migrate a stock stack](/plunge/use/migrate/)).
A type with no tags argument, such as a subnet, is record-owned.

## What has run

Nothing on Azure. Adoption is unit-tested, and the corpus run waits on
[#7](https://github.com/INTENTIUS/plunge/issues/7).
