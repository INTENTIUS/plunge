---
title: Set up by hand
description: The bucket settings each store checks when it opens, and the credentials it reads.
prompt: |
  Follow https://intentius.io/plunge/use/setup/. Check our record bucket's settings with read-only CLI calls and report which ones the store would refuse. Change nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

plunge creates no bucket; you create it, and the store checks it on every
open.

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

A failing or unreadable setting refuses the open (`ContractRefusedError`)
and names the fix. To waive one, list it in the `allow_insecure` query key or
`PLUNGE_ALLOW_INSECURE`, comma separated. A lifecycle rule deleting current
records cannot be waived. GCS's `list_isolation` check only warns.

## Credentials

S3 uses the AWS SDK's default chain with the `profile` and `region` query
keys, and GCS uses application default credentials. Azure Blob uses
azidentity's default chain against real Azure. Its settings are read through
ARM, which needs `resource_group` and `subscription_id` (or
`AZURE_RESOURCE_GROUP` and `AZURE_SUBSCRIPTION_ID`).

Given an `endpoint` or `metadata_host` query key, `plunge+azblob://` talks only
to the URL's endpoints and ignores the shell's Azure login
([#35](https://github.com/INTENTIUS/plunge/issues/35)). It signs with
`ARM_ACCESS_KEY` or a SAS token or client secret taken from the environment
only, since the backend URL is saved and printed. The stack metadata goes
through the DIY backend's go-cloud driver, which reads only
`AZURE_STORAGE_ACCOUNT` with `AZURE_STORAGE_KEY` or `AZURE_STORAGE_SAS_TOKEN`;
without them the open is refused, since go-cloud would build Azure's default
chain. The endpoint must be local (`localhost`, `*.localhost` or a loopback
address), with the account in the path on `127.0.0.1` or `localhost`:
`http://127.0.0.1:10000/<account>`.

Against an emulator, S3 and GCS follow the same rule
([#50](https://github.com/INTENTIUS/plunge/issues/50)) and never build the SDK's
default chain, which could ask a metadata server (EC2 or GCE) or an identity
endpoint (SSO, STS or Google's OAuth) for a credential:

- `plunge+s3://` with an `endpoint` query key (or `AWS_ENDPOINT_URL_S3` or
  `AWS_ENDPOINT_URL` set) signs only with `AWS_ACCESS_KEY_ID` and
  `AWS_SECRET_ACCESS_KEY`, plus any `AWS_SESSION_TOKEN`. A missing key or a
  `profile` in the URL refuses the open. The region comes from `region`, then
  `AWS_REGION`, then defaults to `us-east-1`.
- `plunge+gs://` with `STORAGE_EMULATOR_HOST` set sends
  `GOOGLE_OAUTH_ACCESS_TOKEN` as a static token, or no credential. The open is
  refused unless `GOOGLE_OAUTH_ACCESS_TOKEN` or `GOOGLE_CREDENTIALS` is set,
  because the DIY backend holding the stack metadata would look up application
  default credentials. An emulator taking anonymous calls accepts any token. An
  `endpoint` query key is refused; use `STORAGE_EMULATOR_HOST`, which the DIY
  backend also follows.

Otherwise S3 and GCS use their default chains against the real cloud.

## Kubernetes

`plunge+k8s://` keeps one Secret per record in the namespace and needs a DIY
`metadata` URL for the stack metadata. The Secrets carry an
[estate](../../glossary/#estate) label, which choudoufu's admission policy
reads.

## Who may write

The identity running plunge needs read and write on the record prefix and
read on the bucket's settings. [Record store bucket](../bucket/) has commands
for a bucket that passes.

Source: choudoufu's `pkg/staterecord/estate-boundary.yaml`, the admission
policy plunge imports.
