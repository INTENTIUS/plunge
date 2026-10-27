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

The corpus runs pulumi-aws 7.51.1.

## Identifiers

Cloud Control returns each type's primary identifier, which is not always
pulumi-aws's ID. A multi-part identifier is translated where choudoufu's
`pkg/identity` spells the Terraform ID from those parts (`ccid.go`), or by a
hand-written rule (`ccid_hand.go`). Translations for single-part identifiers
(`ccid_single.go`, [#28](https://github.com/INTENTIUS/plunge/issues/28)) and for `aws_budgets_budget_action`, which needs
the account ID from the stack's own STS ([#31](https://github.com/INTENTIUS/plunge/issues/31)), are built with tests written
and not run.

## Types with no tags

A type with no tags argument is record-owned: the record carries ownership,
and its `ownership` entry says why. `aws:s3/bucketPolicy:BucketPolicy` is one;
in the recorded `aws-ts` run it carried no markers ([Claims and runs](/plunge/claims/)).

## Refused

- a provider whose `ignoreTags` or `defaultTags` would hide the `plunge-` keys
- an aws-native resource with create-only tags, at adoption
- a random `pulumi:autonaming` pattern on a covered type, unless `PLUNGE_ALLOW_RANDOM_AUTONAMES=true`

The full list of what C1 leaves open, with each record reason, is in
[CONCURRENCY.md](https://github.com/INTENTIUS/plunge/blob/main/docs/plunge/CONCURRENCY.md).
