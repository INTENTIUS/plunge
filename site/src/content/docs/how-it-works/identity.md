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

Label values are short and lower-case, so labels carry a digest. It is
enough to bind an object or detect a collision; the record holds the full URN.

A URN longer than one tag value (256 characters on AWS and Azure) continues
in `plunge-urn-2` up to `plunge-urn-4`, as choudoufu's `tofu-address`
continuation tags do.

In the recorded `aws-ts` run, bucket `docs-assets` (a child of component
`docs`) carries `plunge-stack = corpus-aws-ts/floci` and
`plunge-urn = corpus:index:Site$aws:s3/bucket:Bucket::docs-assets`
([Claims and runs](../../claims/)).

## Where the marker goes

Pulumi's schema has no "taggable" flag, so plunge decides per provider
family (`pkg/backend/plunge/catalog.go`):

- pulumi-aws, pulumi-azure and pulumi-gcp serve their Terraform schema through the bridge; a type is markable when it has a tags (for Google, labels) argument.
- aws-native's committed `metadata.json` at the provider's version names the tags property and whether it is create-only.
- Every Kubernetes object carries `metadata.labels`; Helm, YAML and Kustomize resources have none of their own and are left alone.
- azure-native's schema is read once per provider version and cached (`PLUNGE_AZURE_NATIVE_CACHE`).

plunge writes map tags and aws-native's key/value arrays; other tag shapes
are recorded and left alone.

## When the record holds ownership

A type is record-owned when it has no tags argument, an unwritable tags
shape or an unreadable schema, or when choudoufu's `pkg/identity` classes it
markerless. Its record's `ownership` entry says `"by": "record"` and why; the
bucket policy in the recorded `aws-ts` run is one.

## Refusals

plunge refuses by name a provider whose `ignoreTags`, `ignoreLabels`,
`defaultTags` or `defaultLabels` would hide or override the markers, and a
resource whose tags are create-only (aws-native `tagUpdatable: false`), since
marking it would replace it.

## Who may change a resource

The cloud's own policy can condition on the marker. None has run against
plunge's markers yet; each platform's Gate page shows the shape and [#45](https://github.com/INTENTIUS/plunge/issues/45)
holds the run.
