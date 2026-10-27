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

Each resource keeps its ID and gets a record; the next `plunge up` adds the
tags ([Adopt an existing stack](/plunge/use/migrate/)). A type with no tags
argument, such as a subnet, is record-owned.
