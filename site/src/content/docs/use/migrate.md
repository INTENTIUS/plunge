---
title: Adopt an existing stack
description: Adopt a stock Pulumi stack onto per-resource records with plunge stack adopt, and back out with a stock import.
prompt: |
  Follow https://intentius.io/plunge/use/migrate/. Run `plunge stack adopt --json --stack <stack>` without --yes and report the status, each resource's fate and any refusal.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

`plunge stack adopt` gives every resource of a stock stack its own
[record](../../glossary/#record), with the checkpoint's IDs unchanged, and
makes no cloud calls. The stack then behaves as a plunge stack, with the
differences [below](#differences-from-stock-pulumi).

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

Adoption is refused before any write when the checkpoint has pending
operations, or when a resource has create-only tags, which marking would
replace. It is also refused for a provider that would hide or override the
markers: an `ignoreTags` key or prefix matching `plunge-`, or a `defaultTags`
or `defaultLabels` entry with a `plunge-` key. Re-adopting the
same checkpoint does nothing (`already-adopted`); a checkpoint changed since is
refused (`adopted-from-another-checkpoint`).

## 4. The first update

The next `plunge up` adds `plunge-stack` and `plunge-urn` to each taggable
resource in place. Those tags are the only change it makes to the cloud
objects; the list below covers how the stack behaves from then on. Before
that update, `plunge destroy` is refused for each unmarked taggable object
([Deletes](../../how-it-works/deletes/)).

## Differences from stock Pulumi

- Replacing a resource whose name plunge chose deletes the old object before
  it creates the new one, so for a short window neither exists
  ([Names](../../how-it-works/names/#replacing-a-named-resource),
  [#12](https://github.com/INTENTIUS/plunge/issues/12)).
- plunge keeps no stack history and no checkpoint backups.
  `plunge stack history`, `plunge stack export --version` and
  `plunge stack rm --remove-backups` are refused by name; bucket versioning
  keeps earlier versions of each record.
- `plunge preview` and `plunge up` refresh from the cloud first, by default.
  `PLUNGE_REFRESH=false` turns that off.
- A new resource the program leaves unnamed gets a fixed name, its name plus
  seven hex digits, in place of a random suffix. Adopted resources keep their
  names ([Names](../../how-it-works/names/)).
- A delete, `plunge destroy` included, is refused when a taggable object has
  lost its markers ([Deletes](../../how-it-works/deletes/)).
- The backend URL gains a `plunge+` prefix, and each taggable resource gets
  `plunge-stack` and `plunge-urn` tags.

## Back out

```sh
plunge stack export --stack dev --file s.json
pulumi login s3://my-state-bucket     # stock pulumi, a stock backend
pulumi stack import --stack dev --file s.json
pulumi preview --refresh --stack dev
```

The preview should show only the removal of the `plunge-*` tags.

Source: `pkg/backend/plunge/adopt.go`.
