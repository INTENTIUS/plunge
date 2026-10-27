---
title: Compatibility
description: Which AWS providers and types take markers, which plunge names, and how Cloud Control lists them.
prompt: |
  Follow https://intentius.io/plunge/aws/compatibility/. List the AWS resource types in this program and say for each whether it takes markers and whether C1 covers it. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Providers

| Provider | Markers | Names (C1) | Listing (C2) |
|---|---|---|---|
| pulumi-aws | map `tags`, from the bridge's Terraform schema | where the bridge marks a field autonamed, it forces replacement, and `pkg/identity` builds the type's identity from it | Cloud Control `ListResources`, collision claim made |
| aws-native | map or key/value-array tags, from the embedded `metadata.json` (v1.83.1) | where the `autoNamingSpec` property is the `primaryIdentifier` (206 types) | Cloud Control, collision claim made |

The corpus programs run pulumi-aws at version 7.51.1, alongside aws-native.

## Identifiers

Cloud Control's primary identifier is not always pulumi-aws's ID. choudoufu's
`pkg/identity` translates a multi-part identifier from its parts or by a
hand-written rule, and single-part identifiers too
([#28](https://github.com/INTENTIUS/plunge/issues/28)), in
`CloudControlTranslation`
([choudoufu#2286](https://github.com/INTENTIUS/choudoufu/issues/2286)).
`aws_budgets_budget_action` needs the account ID, which plunge reads from the
stack's own STS ([#31](https://github.com/INTENTIUS/plunge/issues/31)). Both
issues are closed with their tests passing.

## Types with no tags

A type with no tags argument is record-owned, with the reason in its
`ownership` entry. `aws:s3/bucketPolicy:BucketPolicy` carried no markers in the
recorded `aws-ts` run ([Claims and runs](/plunge/claims/)).

## Refused

- a provider whose `ignoreTags` key or prefix matches `plunge-`, or whose `defaultTags` has an entry with a `plunge-` key
- an aws-native resource with create-only tags, at adoption
- a random `pulumi:autonaming` pattern on a covered type, unless `PLUNGE_ALLOW_RANDOM_AUTONAMES=true`

The full list of types C1 leaves open, with each record reason, is in
[CONCURRENCY.md](https://github.com/INTENTIUS/plunge/blob/main/docs/plunge/CONCURRENCY.md).
