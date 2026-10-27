---
title: Records
description: What a record holds, the five stores, how each write is conditioned, and what the store checks when it opens.
prompt: |
  Read https://intentius.io/plunge/how-it-works/records/ and say which plunge+ store fits this team, and which query keys its URL needs. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

A plunge stack's state is a set of records in a store with conditional
writes. The store is choudoufu's public `staterecord` package
(`github.com/intentius/choudoufu/pkg/staterecord`), which plunge imports as a module.

## What a record is

| Key under `stacks/<project>/<stack>/` | Holds |
|---|---|
| `resources/<urn>` | every state for one URN (the live one and any pending delete), and the pending operations on it |
| `outputs/<name>` | one root stack output |
| `secrets` | the secrets configuration, created once and never rewritten |
| `snippets/<uuid>`, `extensions/<ref>` | one PCL snippet, one extension blob |

A run writes only the records whose content changed since it read them. A
secret re-encrypted to a new ciphertext is compared by its plaintext, so it is
no change.

## Records hold the full state

A resource record holds what a checkpoint would hold for that resource:
inputs, outputs, and secret values as ciphertext under the stack's secrets
provider. Nothing in it is a cache you may lose, which differs from
choudoufu, whose records hold only what a read of the live object leaves out.
[#38](https://github.com/INTENTIUS/plunge/issues/38) asked whether plunge
should follow; its report recommends keeping full records. Guard the store as
you guard a state file.

## The five stores

| URL | Store | Conditional write |
|---|---|---|
| `plunge+file:///path` | local files, one host only | a hard link that fails on an existing name; updates compare under a lock file held for one write |
| `plunge+s3://bucket/prefix` | S3 | `If-None-Match: *` to create, `If-Match` on the version read to update or delete |
| `plunge+gs://bucket/prefix` | Cloud Storage | a does-not-exist precondition, then `ifGenerationMatch` |
| `plunge+azblob://container/prefix` | Azure Blob Storage | `If-None-Match: *`, then `If-Match` |
| `plunge+k8s://namespace/prefix` | one Kubernetes Secret per record | a create that fails on an existing Secret, then the Secret's `resourceVersion` |

For every store but Kubernetes, the URL after `plunge+` is a DIY backend URL,
and the records sit under `.pulumi/records` beside its metadata. The DIY
backend has no Kubernetes store, so `plunge+k8s://` names one for the
metadata in its `metadata` query key
(`plunge+k8s://records?metadata=s3://bucket/meta`); a URL without one is
refused (`MissingMetadataURLError`). Its Secrets carry an estate label (the
`estate` query key, else the namespace) that choudoufu's estate-boundary
admission policy fences writes with. The query keys for each store are in the
[Reference](../../reference/#backend-urls).

## Checks when the store opens

S3, GCS and Azure Blob stores read the bucket's settings when they open and
refuse by name (`ContractRefusedError`, with the fix) when one fails or cannot
be read:

| Store | Settings checked |
|---|---|
| S3 | `versioning`, `lifecycle` (a rule expiring noncurrent versions under the records' prefix), `public_access_block` |
| GCS | `object_versioning`, `noncurrent_lifecycle`, `public_access_prevention`, `uniform_bucket_level_access`; `list_isolation` only warns |
| Azure Blob | `blob_versioning`, `lifecycle_management` (a rule deleting previous versions), `public_access` |

Naming a setting in the `allow_insecure` query key or `PLUNGE_ALLOW_INSECURE`
proceeds past it. A lifecycle rule that deletes current records cannot be
waived. [Record store bucket](../../use/bucket/) gives the commands.

## How a stack is read

The snapshot is assembled from the records on every read, each resource after
its parent, its provider and everything it depends on. A resource that refers
to something no record holds is refused (`MissingReferenceError`, naming the
resource, the reference and the record key).

## No history

There is no single update for a history entry to describe, so plunge keeps no
stack history or checkpoint backups. `plunge stack history`,
`plunge stack export --version` and `plunge stack rm --remove-backups` are
refused (`UnsupportedOnRecordsError`). A bucket with object versioning keeps
each record's earlier versions.

## What has run

The local store ran in the recorded `aws-ts` runs ([Claims and runs](../../claims/)).
The S3, GCS, Azure Blob and Kubernetes stores are unit-tested, and their runs
on emulators wait on [#6](https://github.com/INTENTIUS/plunge/issues/6) and
[#14](https://github.com/INTENTIUS/plunge/issues/14).
