---
title: Records
description: Record contents, the five stores, each store's conditional write, and the checks a store runs when it opens.
prompt: |
  Read https://intentius.io/plunge/how-it-works/records/ and say which plunge+ store fits this team, and which query keys its URL needs. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

A plunge stack's state is a set of records in a store with conditional
writes: choudoufu's public `staterecord` package
(`github.com/intentius/choudoufu/pkg/staterecord`), imported as a module.

## Record keys

| Key under `stacks/<project>/<stack>/` | Holds |
|---|---|
| `resources/<urn>` | every state for one URN (the live one and any pending delete), and the pending operations on it |
| `outputs/<name>` | one root stack output |
| `secrets` | the secrets configuration, created once and never rewritten |
| `snippets/<uuid>`, `extensions/<ref>` | one PCL snippet, one extension blob |

A run writes only records whose content changed since it read them. A
re-encrypted secret is compared by plaintext, so a new ciphertext is no change.

## Records hold the full state

A resource record holds its checkpoint content: inputs, outputs, and secrets
as ciphertext under the stack's secrets provider. None of it is a disposable
cache, unlike choudoufu, whose records hold only the state a live read leaves
out; [#38](https://github.com/INTENTIUS/plunge/issues/38)'s report recommends
keeping full records. Guard the store like a state file.

## The five stores

| URL | Store | Conditional write |
|---|---|---|
| `plunge+file:///path` | local files, one host only | a hard link that fails on an existing name; updates compare under a lock file held for one write |
| `plunge+s3://bucket/prefix` | S3 | `If-None-Match: *` to create, `If-Match` on the version read to update or delete |
| `plunge+gs://bucket/prefix` | Cloud Storage | a does-not-exist precondition, then `ifGenerationMatch` |
| `plunge+azblob://container/prefix` | Azure Blob Storage | `If-None-Match: *`, then `If-Match` |
| `plunge+k8s://namespace/prefix` | one Kubernetes Secret per record | a create that fails on an existing Secret, then the Secret's `resourceVersion` |

Except for Kubernetes, the URL after `plunge+` is a DIY backend URL, with
records under `.pulumi/records` beside its metadata. The DIY backend has no
Kubernetes store, so `plunge+k8s://` needs a `metadata` query key
(`plunge+k8s://records?metadata=s3://bucket/meta`) or is refused
(`MissingMetadataURLError`). Its Secrets carry an estate label (the `estate`
key, else the namespace) that choudoufu's estate-boundary admission policy
fences writes with. Query keys are in the
[Reference](../../reference/#backend-urls).

## Checks when the store opens

S3, GCS and Azure Blob stores read the bucket's settings on open and refuse
by name (`ContractRefusedError`, with the fix) when one fails or is unreadable:

| Store | Settings checked |
|---|---|
| S3 | `versioning`, `lifecycle` (a rule expiring noncurrent versions under the records' prefix), `public_access_block` |
| GCS | `object_versioning`, `noncurrent_lifecycle`, `public_access_prevention`, `uniform_bucket_level_access`; `list_isolation` only warns |
| Azure Blob | `blob_versioning`, `lifecycle_management` (a rule deleting previous versions), `public_access` |

A setting named in the `allow_insecure` query key or `PLUNGE_ALLOW_INSECURE`
is skipped, except a lifecycle rule deleting current records.
[Record store bucket](../../use/bucket/) gives the commands.

A store URL naming an emulator (`endpoint` on S3 or Azure Blob,
`metadata_host` on Azure Blob, `STORAGE_EMULATOR_HOST` for GCS) signs only with
environment credentials, never the SDK's default chain, so it asks no metadata
server, SSO, STS or OAuth host. Without them the open is refused by name
([#35](https://github.com/INTENTIUS/plunge/issues/35),
[#50](https://github.com/INTENTIUS/plunge/issues/50); variables on
[Setup](../../use/setup/#credentials)). The stack metadata is checked the same
way; on Azure Blob it goes to the record store's local endpoint with
`AZURE_STORAGE_KEY` or `AZURE_STORAGE_SAS_TOKEN`.

## How a stack is read

Every read assembles the snapshot from the records in dependency order, each
resource after its parent and provider. A reference no record holds is refused
(`MissingReferenceError`, naming the resource, reference and record key).

## No history

No single update exists for a history entry to describe, so plunge keeps no
stack history or backups and refuses `plunge stack history`,
`plunge stack export --version` and `plunge stack rm --remove-backups`
(`UnsupportedOnRecordsError`). Bucket versioning keeps earlier record versions.

## Recorded runs

The local store ran in the recorded `aws-ts` runs ([Claims and runs](../../claims/)).
