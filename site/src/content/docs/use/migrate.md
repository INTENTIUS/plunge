---
title: Migrate a stock stack
description: Adopt a stock Pulumi stack onto per-resource records with plunge stack adopt, and back out with a stock import.
prompt: |
  Follow https://intentius.io/plunge/use/migrate/. Run `plunge stack adopt --json --stack <stack>` without --yes and report the status, each resource's fate and any refusal.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

`plunge stack adopt` moves a stock stack onto plunge's records
(`pkg/backend/plunge/adopt.go`). Every resource's state becomes its own record
with the checkpoint's IDs unchanged. Nothing is read from or written to the
cloud.

## 1. Point plunge at the same root

Until the stack is adopted, a plunge run against it is refused
(`AdoptionRequiredError`) rather than seeing an empty stack and creating
everything again.

Log in to the DIY root your stack lives in, with `plunge+` in front:

```sh
plunge login plunge+s3://my-state-bucket   # the stock stack is at s3://my-state-bucket
```

By default the checkpoint adopted is the one the DIY backend at that root
holds for the stack. `--file` adopts an exported deployment instead, which is
the way in from Pulumi Cloud (`pulumi stack export --file s.json`).

## 2. See what would happen

```sh
plunge stack adopt --json --stack dev
```

This writes nothing. It prints one JSON document
([The adopt document](../../reference/adopt-json/)): the status
`would-adopt` and each resource's fate. On AWS every resource is `pending` at
this point, since adoption loads no provider schema; the first run classes it.

## 3. Adopt

```sh
plunge stack adopt --stack dev          # asks first
plunge stack adopt --json --yes --stack dev
```

Adoption is refused before anything is written if the checkpoint has pending
operations or a provider's `ignoreTags` or `defaultTags` would hide the
markers. A resource whose tags can only be set at create is refused too, since
marking it would replace it. Adopting the
same checkpoint again does nothing (`already-adopted`). A checkpoint changed
after adoption is refused (`adopted-from-another-checkpoint`).

## 4. The first update

The next `plunge up` adds `plunge-stack` and `plunge-urn` to each taggable
resource in place. That is the only change adoption brings. Until then the
objects carry no markers, so a `plunge destroy` is refused for each taggable
one ([Deletes](../../how-it-works/deletes/)).

## Back out

```sh
plunge stack export --stack dev --file s.json
pulumi login s3://my-state-bucket     # stock pulumi, a stock backend
pulumi stack import --stack dev --file s.json
pulumi preview --refresh --stack dev
```

The preview should show only the removal of the `plunge-*` tags.

## What has run

Steps 2 and 3 ran on 2026-10-26 against a stock pulumi v3.272.0 stack of
`aws-ts` on floci: `would-adopt`, `adopted`, `already-adopted`, and
`refused` after a stock refresh changed the checkpoint (behold's
[fixture](https://github.com/INTENTIUS/behold/tree/main/src/__fixtures__/plunge-adopt-30806be26b)).
The first update after adoption and the back-out are unit-tested
(`TestBackOutLeavesOnlyMarkerTagRemovals`) and wait for their run on
[#7](https://github.com/INTENTIUS/plunge/issues/7).
