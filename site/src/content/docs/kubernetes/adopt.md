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

Every object keeps its ID and gets its own record. The next `plunge up` adds
the two labels to each object's `metadata.labels` in place
([Migrate a stock stack](/plunge/use/migrate/)).

Helm releases and the YAML and Kustomize resources have no object metadata of
their own to label, so they are left unmarked. A provider setting that would
hide labels starting with `plunge-` is refused by name.

## What has run

Nothing on Kubernetes. No corpus program uses pulumi-kubernetes. Adoption is
unit-tested on other families, and the corpus run waits on [#7](https://github.com/INTENTIUS/plunge/issues/7).

## Leaving

[Back out](/plunge/use/migrate/#back-out) with `plunge stack export` and a
stock `pulumi stack import`.
