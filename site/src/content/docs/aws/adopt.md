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

Each resource keeps its ID and gets a record. The next `plunge up` tags each
taggable resource with `plunge-stack` and `plunge-urn` in place
([Migrate](/plunge/use/migrate/)).

## Refusals

A pulumi-aws provider is refused (`marker-hiding-provider-config`) when an
`ignoreTags` key or prefix matches `plunge-`, or when a `defaultTags` entry
has a `plunge-` key. Other default tags are fine. So
is an aws-native resource with create-only tags (`tagUpdatable: false`),
which marking would replace (`create-only-tags`).

## Types with no tags

A type with no tags argument, such as `aws:s3/bucketPolicy:BucketPolicy`,
is record-owned, with fate `pending` at adoption until the first run classes
it from the schema.

With no provider schema loaded, every resource is `pending`.

## Leaving

[Back out](/plunge/use/migrate/#back-out) with `plunge stack export` and a
stock `pulumi stack import`.
