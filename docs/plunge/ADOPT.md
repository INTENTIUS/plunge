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
| `adopt` | The document version. A change to the shape is a new version. |
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

A consumer should treat an unknown code as a refusal it cannot explain, and an
unknown `adopt` version as a document it cannot read.
