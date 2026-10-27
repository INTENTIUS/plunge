---
title: Migrate a stock stack
description: Adopt a stock Pulumi stack onto per-resource records with plunge stack adopt, and back out with a stock import.
prompt: |
  Follow https://intentius.io/plunge/use/migrate/. Run `plunge stack adopt --json --stack <stack>` without --yes and report the status, each resource's fate and any refusal.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

`plunge stack adopt` (`pkg/backend/plunge/adopt.go`) gives every resource of a
stock stack its own record, with the checkpoint's IDs unchanged, and makes no
cloud calls.

## 1. Point plunge at the same root

Until adoption, plunge refuses to run against the stack
(`AdoptionRequiredError`) instead of recreating everything. Log in to the
stack's DIY root with `plunge+` in front:

```sh
plunge login plunge+s3://my-state-bucket   # the stock stack is at s3://my-state-bucket
```

By default plunge adopts the stack's checkpoint at that root. `--file` adopts
an exported deployment instead, the way in from Pulumi Cloud
(`pulumi stack export --file s.json`).

## 2. Preview the adoption

```sh
plunge stack adopt --json --stack dev
```

This writes nothing and prints the status `would-adopt` and each resource's
fate as JSON ([The adopt document](../../reference/adopt-json/)). On AWS each
resource is `pending`, since adoption loads no provider schema; the first run
classes it.

## 3. Adopt

```sh
plunge stack adopt --stack dev          # asks first
plunge stack adopt --json --yes --stack dev
```

Adoption is refused before any write for pending operations in the
checkpoint, a provider `ignoreTags` or `defaultTags` hiding the markers, or a
resource with create-only tags, which marking would replace. Re-adopting the
same checkpoint does nothing (`already-adopted`); a checkpoint changed since is
refused (`adopted-from-another-checkpoint`).

## 4. The first update

The next `plunge up` adds `plunge-stack` and `plunge-urn` to each taggable
resource in place, the only change adoption brings. Before it, `plunge destroy`
is refused for each unmarked taggable object ([Deletes](../../how-it-works/deletes/)).

## Back out

```sh
plunge stack export --stack dev --file s.json
pulumi login s3://my-state-bucket     # stock pulumi, a stock backend
pulumi stack import --stack dev --file s.json
pulumi preview --refresh --stack dev
```

The preview should show only the removal of the `plunge-*` tags.

## Recorded runs

Steps 2 and 3 ran on 2026-10-26 on a stock pulumi v3.272.0 `aws-ts` stack on
floci: `would-adopt`, `adopted`, `already-adopted`, and `refused` once a stock
refresh changed the checkpoint (behold's
[fixture](https://github.com/INTENTIUS/behold/tree/main/src/__fixtures__/plunge-adopt-30806be26b)).
The first update after adoption and the back-out are unit-tested
(`TestBackOutLeavesOnlyMarkerTagRemovals`) and wait on
[#7](https://github.com/INTENTIUS/plunge/issues/7).
