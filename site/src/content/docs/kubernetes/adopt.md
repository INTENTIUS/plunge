---
title: Adopt
description: Move a stock Pulumi stack of Kubernetes objects onto plunge records.
prompt: |
  Follow https://intentius.io/plunge/kubernetes/adopt/. Run `plunge stack adopt --json --stack <stack>` without --yes and report the status and every refusal.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
```
plunge stack adopt --json --stack dev        # classify, write nothing
plunge stack adopt --json --yes --stack dev  # adopt
```

Each object keeps its ID and gets a record. The next `plunge up` adds both
labels to `metadata.labels` in place ([Migrate](/plunge/use/migrate/)).

Helm releases and YAML and Kustomize resources have no metadata of their own
and stay unmarked. A provider setting hiding `plunge-` labels is refused by name.

## Recorded runs

No corpus program uses pulumi-kubernetes, so none. Adoption is unit-tested
on other families; the corpus run waits on [#7](https://github.com/INTENTIUS/plunge/issues/7).

## Leaving

[Back out](/plunge/use/migrate/#back-out) with `plunge stack export` and a
stock `pulumi stack import`.
