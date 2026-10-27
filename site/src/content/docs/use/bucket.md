---
title: Record store bucket
description: Create an S3 or GCS bucket the record store opens without a waiver.
prompt: |
  Follow https://intentius.io/plunge/use/bucket/. Show me the commands to create our record bucket, filled in with the names I give. Run nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

These commands set up the settings the store checks on open
([Set up by hand](../setup/)), as plunge's own scripts do.

## S3

```sh
B=my-records
aws s3api create-bucket --bucket $B
aws s3api put-bucket-versioning --bucket $B --versioning-configuration Status=Enabled
aws s3api put-bucket-lifecycle-configuration --bucket $B --lifecycle-configuration \
  '{"Rules":[{"ID":"records-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}'
aws s3api put-public-access-block --bucket $B --public-access-block-configuration \
  BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
```

With `bucket_owner=<account id>` in the URL, every request carries S3's
`ExpectedBucketOwner`.

## GCS

```sh
B=my-records
echo '{"rule":[{"action":{"type":"Delete"},"condition":{"daysSinceNoncurrentTime":30}}]}' > lifecycle.json
gcloud storage buckets create gs://$B --uniform-bucket-level-access --public-access-prevention
gcloud storage buckets update gs://$B --versioning --lifecycle-file lifecycle.json
```

## Azure Blob

Enable blob versioning, add a lifecycle rule deleting previous versions, and
disable anonymous blob access. The store reads these through ARM using the
URL's `storage_account`, `resource_group` and `subscription_id`.
