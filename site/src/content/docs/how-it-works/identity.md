---
title: Identity
description: The two markers on each resource, how they are written on tags and on labels, and which types the record holds instead.
prompt: |
  Read https://intentius.io/plunge/how-it-works/identity/ and list which resource types in this program take markers and which would be held by their record. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Two marker keys mark a resource as a stack's (`pkg/backend/plunge/markers/keys.go`).

| Key | Value on tags (AWS, aws-native, Azure) | Value on labels (Google Cloud, Kubernetes) |
|---|---|---|
| `plunge-stack` | `<project>/<stack>` | a 16-character hex digest of it |
| `plunge-urn` | the URN without its `urn:pulumi:<stack>::<project>::` prefix | a 32-character hex digest of the URN |

Label values are short and lower-case, so the label families carry a digest.
A digest is enough to bind an object and to detect a collision; the full URN
is in the record.

A URN longer than one tag value (256 characters on AWS and Azure) continues
in `plunge-urn-2` up to `plunge-urn-4`, as choudoufu's `tofu-address`
continuation tags do.

In the recorded `aws-ts` run, the bucket `docs-assets`, a child of the
component `docs`, carries `plunge-stack = corpus-aws-ts/floci` and
`plunge-urn = corpus:index:Site$aws:s3/bucket:Bucket::docs-assets`
([Claims and runs](../../claims/)).

## Where the marker goes

Pulumi's schema has no "taggable" flag, so plunge decides per provider family
from the schema (`pkg/backend/plunge/catalog.go`):

- pulumi-aws, pulumi-azure and pulumi-gcp serve their Terraform schema through the bridge. The type's tags (labels, for Google) argument says whether it can be marked.
- aws-native's committed `metadata.json` at the provider's version names the tags property and whether it is create-only.
- Every Kubernetes object carries `metadata.labels`. Helm, YAML and Kustomize resources have no object metadata of their own and are left alone.
- azure-native's schema is read once per provider version and cached (`PLUNGE_AZURE_NATIVE_CACHE`).

plunge writes tags that are a map, and aws-native's key/value arrays. A tags
property of any other shape is recorded and left alone.

## When the record holds ownership

A type with no tags argument, a tags shape plunge does not write, a schema
that could not be read, or a type choudoufu's `pkg/identity` classes
markerless is record-owned: its record's `ownership` entry says
`"by": "record"` and why. The recorded `aws-ts` run's bucket policy is one.

## What is refused

A provider whose `ignoreTags`, `ignoreLabels`, `defaultTags` or
`defaultLabels` would hide or override the markers is refused by name, and so
is a resource whose tags can only be set at create (aws-native
`tagUpdatable: false`), since marking it would replace it.

## Who may change a resource

The marker is a tag or label, so the cloud's own policy can condition on it.
No such policy has been run against plunge's markers yet; each platform's
Gate page shows the shape and [#45](https://github.com/INTENTIUS/plunge/issues/45)
holds the run.
