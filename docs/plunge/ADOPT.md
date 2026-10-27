# `plunge stack adopt --json`

`plunge stack adopt` moves a stock Pulumi stack onto plunge's per-resource
records (see `pkg/backend/plunge/adopt.go`). With `--json` it prints what the
adoption did or would do as one JSON document on stdout, and never prompts.

- `--json` alone classifies the checkpoint and writes nothing: no resource
  record, no adoption record, no other store write.
- `--json --yes` adopts and prints the result.
- A refused adoption prints the document with its refusals and exits 1, with
  nothing on stderr. Any other failure (no plunge backend, an unreadable
  checkpoint) is an ordinary error on stderr and no document.

## Document, version v1

```json
{
  "adopt": "v1",
  "stack": "organization/app/dev",
  "status": "would-adopt",
  "checkpoint": {
    "source": "the DIY checkpoint at file:///srv/state",
    "digest": "3f5c...e1"
  },
  "resources": [
    {
      "urn": "urn:pulumi:dev::app::aws:s3/bucket:Bucket::logs",
      "type": "aws:s3/bucket:Bucket",
      "id": "logs",
      "fate": "pending",
      "marked": false,
      "reason": "classed at the first run, from the provider's schema"
    }
  ],
  "refusals": []
}
```

| Field | Meaning |
|---|---|
| `adopt` | The document version. Removing or reinterpreting a field is a new version; the optional fields `--adopt` adds (below) are not. |
| `stack` | The stack reference. |
| `status` | `would-adopt` (classified, nothing written), `adopted` (this call wrote the records), `already-adopted` (the stack's adoption record names this checkpoint; nothing written), or `refused` (nothing written; the command exits 1). |
| `checkpoint.source` | Where the checkpoint came from: the DIY checkpoint at the same root, or `the file <path>` for `--file`. For `already-adopted`, the source the adoption record names. Absent when the checkpoint was not read. |
| `checkpoint.digest` | SHA-256 of the checkpoint's deployment in canonical JSON, the digest the adoption record holds. |
| `resources` | One entry per custom, non-provider resource of the checkpoint, sorted by URN. Always a list. Empty for a refusal reached before classification. |
| `resources[].urn`, `.type`, `.id` | The resource as the checkpoint holds it. The ID is kept unchanged by adoption. |
| `resources[].fate` | `marker` (ownership is carried by plunge-* tags the next `plunge up` adds in place), `record` (an untaggable type; the record alone carries ownership), `pending` (classed at the first run from the provider's schema, which adoption does not load), or `already-adopted`. |
| `resources[].adoptedAs` | For `already-adopted`, the class the adoption record holds (`marker`, `record`, `pending`). |
| `resources[].marked` | The checkpoint's inputs already carry this stack's markers. |
| `resources[].reason` | Why the fate is `record` or `pending`. |
| `resources[].refusals` | The refusals that name this resource, in the shape below. Absent when there are none. |
| `refusals` | Every refusal, in order. Always a list; empty unless `status` is `refused`. |
| `refusals[].code` | One of the codes below. |
| `refusals[].urn` | The resource or provider the refusal is about; absent for a refusal of the whole stack. |
| `refusals[].reason` | The refusal as `plunge stack adopt` prints it without `--json`. |

## Refusal codes

| Code | When | `urn` |
|---|---|---|
| `pending-operations` | The checkpoint has a pending operation; one refusal per operation. Settle it with stock Pulumi first. | the resource of the operation |
| `marker-hiding-provider-config` | A provider's `ignoreTags`, `ignoreLabels`, `defaultTags` or `defaultLabels` would hide or override the plunge-* markers. | the provider |
| `create-only-tags` | A resource's tags can only be set at create (aws-native `tagUpdatable: false`), so marking it would replace it. | the resource |
| `unrepresentable-markers` | A resource's URN cannot be written as a marker value. | the resource |
| `adopted-from-another-checkpoint` | The stack was adopted from a checkpoint with a different digest. | absent |
| `foreign-records` | The stack already has plunge records that are not this checkpoint's. | absent |
| `empty-checkpoint` | The checkpoint holds no resources. | absent |

Refused by `--adopt` only (each names the resource, except `stock-checkpoint`):

| Code | When |
|---|---|
| `engine-type` | The selection names no Pulumi type and the node's inventory type joins to none or several, or the type it names is not one the node's CloudFormation type joins to. |
| `address` | The selection's `as` is not a resource name, or not a top-level URN of this stack. |
| `not-importable` | choudoufu's pkg/identity classes the type as not importable. |
| `import-id` | The node's native ID cannot be turned into the provider's import ID (pkg/identity's Cloud Control translation for pulumi-aws, GoogleAssetImportID for Google). |
| `foreign-marker` | The live object carries another stack's marker, or this stack's marker for another resource. |
| `live-read-failed` | The stack's provider could not be loaded, configured or asked, or a read plunge makes for the import ID (Cloud Control, STS) has no endpoint under the stack's custom endpoints. |
| `not-found` | The stack's provider found no object by the import ID. |
| `stock-checkpoint` | The stack's DIY checkpoint holds resources; adopt it without `--adopt`. |

`create-only-tags`, `marker-hiding-provider-config`, `unrepresentable-markers`,
`foreign-records` and `adopted-from-another-checkpoint` (another adoption
file, or a checkpoint) apply to `--adopt` too.

## `--adopt FILE`

`plunge stack adopt --adopt FILE [--index-store DIR]` adopts indexed cloud
objects (INTENTIUS/plunge#52; design and ruling on choudoufu#2284). FILE is an
`adopt: v1` adoption file (chant core's schema, read through choudoufu
pkg/adoption), which names an index version in the index store (by default
behold's per-user store) and the nodes to adopt. For plunge, `as` is the
resource's name; the resource is adopted top-level, under the root stack.
`type` is the Pulumi type token, and may be left out for an aws node
(aws-native's token for its CloudFormation type) and a Kubernetes node.

Each object is read again through the stack's own default provider,
configured from the stack's configuration, and that read decides. Adoption
writes the resource records, the provider's and the root stack's, and the
adoption record. It writes nothing to the cloud: the next `plunge up` writes
each object's markers as an in-place, tags-only update, after its own preview
and confirmation. The gate is `--yes` (or the prompt) here plus up's
confirmation there.

The document carries, in addition:

| Field | Meaning |
|---|---|
| `index.file`, `.scopeKey`, `.version`, `.digest` | The adoption file and the index version it names. `checkpoint.digest` is the adoption file's digest. |
| `resources[].node` | The index node adopted. |
| `resources[].importId` | The ID the object is imported by. |
| `resources[].markerWrite` | `{property, tags, tagsOnly: true}`: the markers the next `plunge up` sets, and nothing else. Absent for an object already carrying its markers and for a record-owned type. |

The run against an emulator is the maintainer's:
`plunge stack adopt --adopt adopt.json --index-store <dir> --json` on a stack
whose `aws:endpoints` name the pinned floci, then `plunge up`.

A consumer should treat an unknown code as a refusal it cannot explain, and an
unknown `adopt` version as a document it cannot read.
