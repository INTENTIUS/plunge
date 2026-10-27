---
title: What you set up by hand
description: The bucket settings each store checks when it opens, and the credentials it reads.
prompt: |
  Follow https://intentius.io/plunge/use/setup/. Check our record bucket's settings with read-only CLI calls and report which ones the store would refuse. Change nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

plunge creates no bucket. You create it, and the store checks it each time it
opens.

## The bucket

| Store | Must be on | Setting name for `allow_insecure` |
|---|---|---|
| S3 | versioning | `versioning` |
| | a lifecycle rule expiring noncurrent versions under the records' prefix | `lifecycle` |
| | all four public access block flags | `public_access_block` |
| GCS | object versioning | `object_versioning` |
| | a rule deleting noncurrent versions | `noncurrent_lifecycle` |
| | public access prevention enforced | `public_access_prevention` |
| | uniform bucket-level access | `uniform_bucket_level_access` |
| Azure Blob | blob versioning | `blob_versioning` |
| | a lifecycle rule deleting previous versions | `lifecycle_management` |
| | no anonymous blob access | `public_access` |

A setting that fails, or that cannot be read, refuses the open
(`ContractRefusedError`) with the fix. To proceed past one, name it in the
`allow_insecure` query key or `PLUNGE_ALLOW_INSECURE`, comma separated. A
lifecycle rule that deletes current records cannot be waived. GCS's
`list_isolation` check only warns.

## Credentials

S3 uses the AWS SDK's default chain, with the `profile` and `region` query
keys. GCS uses application default credentials. Azure Blob uses azidentity's
default chain against real Azure, and reads its settings through ARM, so it
needs `resource_group` and `subscription_id` (or `AZURE_RESOURCE_GROUP` and
`AZURE_SUBSCRIPTION_ID`).

With an `endpoint` or `metadata_host` query key, `plunge+azblob://` talks
only to the endpoints the URL names and never falls back to the shell's Azure
login ([#35](https://github.com/INTENTIUS/plunge/issues/35)). It signs with
`ARM_ACCESS_KEY`, a SAS token, or a client secret, all from the environment
only, since the backend URL is saved and printed.

## Kubernetes

`plunge+k8s://` keeps one Secret per record in the namespace and needs a DIY
URL for the stack metadata (`metadata`). The Secrets carry an estate label
for choudoufu's estate-boundary admission policy
(`pkg/backend/plunge/staterecord/estate-boundary.yaml`).

## Who may write

The identity running plunge needs read and write on the record prefix and,
for the checks above, read on the bucket's settings. The commands to create a
bucket that passes are on [Record store bucket](../bucket/).
