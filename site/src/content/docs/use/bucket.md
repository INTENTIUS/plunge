---
title: Record store bucket
description: Create an S3 or GCS bucket the record store opens without a waiver.
prompt: |
  Follow https://intentius.io/plunge/use/bucket/. Show me the commands to create our record bucket, filled in with the names I give. Run nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

These are the settings the store checks when it opens
([What you set up by hand](../setup/)). The commands are the ones plunge's
scripts use (`scripts/plunge-races/lib.sh`,
`scripts/plunge-record-store-acceptance.sh`).

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

Add `bucket_owner=<account id>` to the URL to make every request carry S3's
`ExpectedBucketOwner`.

## GCS

```sh
B=my-records
echo '{"rule":[{"action":{"type":"Delete"},"condition":{"daysSinceNoncurrentTime":30}}]}' > lifecycle.json
gcloud storage buckets create gs://$B --uniform-bucket-level-access --public-access-prevention
gcloud storage buckets update gs://$B --versioning --lifecycle-file lifecycle.json
```

## Azure Blob

Turn on blob versioning, add a lifecycle rule that deletes previous versions,
and turn off anonymous blob access on the storage account. Give the URL
`storage_account`, `resource_group` and `subscription_id` so the store can
read those settings through ARM.

## What has run

These commands are in scripts written for
[#6](https://github.com/INTENTIUS/plunge/issues/6) and
[#34](https://github.com/INTENTIUS/plunge/issues/34), which have not run yet.
