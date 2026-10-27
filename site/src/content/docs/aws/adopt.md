---
title: Adopt
description: Move a stock Pulumi stack on AWS onto plunge records with plunge stack adopt.
prompt: |
  Follow https://intentius.io/plunge/aws/adopt/. Run `plunge stack adopt --json --stack <stack>` without --yes and report the status and every refusal.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
```
plunge stack adopt --json --stack dev        # classify, write nothing
plunge stack adopt --json --yes --stack dev  # adopt
```

Every resource keeps its ID and gets its own record. The next `plunge up`
adds `plunge-stack` and `plunge-urn` to each taggable resource's tags in
place ([Migrate a stock stack](/plunge/use/migrate/)).

## What is refused

A pulumi-aws provider whose `ignoreTags` or `defaultTags` would hide or
override the `plunge-` keys is refused by name (`marker-hiding-provider-config`).
So is an aws-native resource whose tags are create-only (`tagUpdatable: false`),
since marking it would replace it (`create-only-tags`).

## Types with no tags

A type with no tags argument, such as `aws:s3/bucketPolicy:BucketPolicy`,
is record-owned: its record carries its ownership, and at adoption its fate is
`pending` until the first run classes it from the schema.

## What has run

On 2026-10-26, against a stock pulumi v3.272.0 stack of the `aws-ts` corpus
program on floci: `would-adopt`, `adopted`, `already-adopted`, and `refused`
with `adopted-from-another-checkpoint` ([Claims and runs](/plunge/claims/)). Every resource was
`pending`, since adoption loads no provider schema.

The first `plunge up` after adoption and the back-out to stock wait for their
run on [#7](https://github.com/INTENTIUS/plunge/issues/7).

## Leaving

[Back out](/plunge/use/migrate/#back-out) with `plunge stack export` and a
stock `pulumi stack import`.
