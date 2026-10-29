---
title: The adopt document
description: The JSON document plunge stack adopt --json prints, version v1, and its refusal codes.
prompt: |
  Read https://intentius.io/plunge/reference/adopt-json/ and check the code in this repository that reads plunge's adopt document against it. Report any field or code it does not handle. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

`plunge stack adopt --json` prints the adoption's result, actual or planned,
as one JSON document on stdout, without prompting. chant and behold read it; the contract is
[docs/plunge/ADOPT.md](https://github.com/INTENTIUS/plunge/blob/main/docs/plunge/ADOPT.md).

- `--json` alone classifies the checkpoint and writes nothing.
- `--json --yes` adopts and prints the result.
- A refusal prints the document and exits 1, stderr empty. Other failures print an error on stderr and no document.

## Version v1

```json
{
  "adopt": "v1",
  "stack": "floci",
  "status": "would-adopt",
  "checkpoint": {
    "source": "the DIY checkpoint at file:///srv/rec-4475/state-adopt",
    "digest": "8a7bd33dc8dfcb2e3a7f476d999c18af484faa71ff89b1d6ba220258e514f6a6"
  },
  "resources": [
    {
      "urn": "urn:pulumi:floci::corpus-aws-ts::aws:iam/role:Role::app-role",
      "type": "aws:iam/role:Role",
      "id": "rec-ts-app",
      "fate": "pending",
      "marked": false,
      "reason": "classed at the first run, from the provider's schema"
    }
  ],
  "refusals": []
}
```

That is the start of a `would-adopt.json`.

| Field | Meaning |
|---|---|
| `adopt` | The document version. A change to the shape is a new version |
| `stack` | The stack reference, as given on the command line |
| `status` | `would-adopt` (nothing written), `adopted` (this call wrote the records), `already-adopted` (the adoption record names this checkpoint), or `refused` (nothing written, exit 1) |
| `checkpoint.source` | The DIY checkpoint at the same root, or `the file <path>` for `--file`. Absent when the checkpoint was not read |
| `checkpoint.digest` | SHA-256 of the checkpoint's deployment in canonical JSON |
| `resources` | One entry per custom, non-provider resource, sorted by URN. Always a list |
| `resources[].fate` | `marker` (the next `plunge up` adds the tags in place), `record` (untaggable; the record carries ownership), `pending` (classed at the first run from the provider's schema), or `already-adopted` |
| `resources[].adoptedAs` | For `already-adopted`, the class the adoption record holds |
| `resources[].marked` | The checkpoint's inputs already carry this stack's markers |
| `resources[].reason` | Why the fate is `record` or `pending` |
| `resources[].refusals` | The refusals naming this resource. Absent when there are none |
| `refusals` | Every refusal, in order. Empty unless `status` is `refused` |

## Refusal codes

| Code | When |
|---|---|
| `pending-operations` | the checkpoint has a pending operation; settle it with stock Pulumi first |
| `marker-hiding-provider-config` | a provider's `ignoreTags` or `ignoreLabels` key or prefix matches `plunge-`, or its `defaultTags` or `defaultLabels` has an entry with a `plunge-` key |
| `create-only-tags` | a resource's tags can only be set at create, so marking it would replace it |
| `unrepresentable-markers` | a resource's URN cannot be written as a marker value |
| `adopted-from-another-checkpoint` | the stack was adopted from a checkpoint with a different digest (recorded in `refused.json`) |
| `foreign-records` | the stack already has records that are not this checkpoint's |
| `empty-checkpoint` | the checkpoint holds no resources |

`--adopt` adds optional fields and eight more codes (`engine-type`,
`address`, `not-importable`, `import-id`, `foreign-marker`,
`live-read-failed`, `not-found`, `stock-checkpoint`), listed in
[ADOPT.md](https://github.com/INTENTIUS/plunge/blob/main/docs/plunge/ADOPT.md#--adopt-file).

Treat an unknown code as an unexplained refusal, and an unknown `adopt`
version as an unreadable document.
