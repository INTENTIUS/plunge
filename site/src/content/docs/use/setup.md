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

S3 and GCS follow the same rule against an emulator
([#50](https://github.com/INTENTIUS/plunge/issues/50)). These stores never build
the SDK's default chain, which could ask the EC2 or GCE metadata server, SSO,
STS or Google's OAuth endpoint for a credential:

- `plunge+s3://` with an `endpoint` query key (or `AWS_ENDPOINT_URL_S3` or
  `AWS_ENDPOINT_URL` set) signs only with `AWS_ACCESS_KEY_ID` and
  `AWS_SECRET_ACCESS_KEY`, plus `AWS_SESSION_TOKEN` when set. The open is
  refused when either key is missing or the URL names a `profile`. The region
  is the `region` key, else `AWS_REGION`, else `us-east-1`.
- `plunge+gs://` with `STORAGE_EMULATOR_HOST` set sends
  `GOOGLE_OAUTH_ACCESS_TOKEN` as a static token, or no credential. The open is
  refused when neither `GOOGLE_OAUTH_ACCESS_TOKEN` nor `GOOGLE_CREDENTIALS` is
  set, because the stack metadata in the same bucket goes through the DIY
  backend, which would otherwise look up application default credentials. Any
  token value works against an emulator that takes anonymous calls. An
  `endpoint` query key is refused; use `STORAGE_EMULATOR_HOST`, the one setting
  the DIY backend also follows.

Without those keys and variables, S3 and GCS use their default chains against
the real cloud, as before.

## Kubernetes

`plunge+k8s://` keeps one Secret per record in the namespace and needs a DIY
URL for the stack metadata (`metadata`). The Secrets carry an estate label
for choudoufu's estate-boundary admission policy
(`pkg/staterecord/estate-boundary.yaml` in choudoufu, which plunge imports).

## Who may write

The identity running plunge needs read and write on the record prefix and,
for the checks above, read on the bucket's settings. The commands to create a
bucket that passes are on [Record store bucket](../bucket/).
