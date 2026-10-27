---
title: Reference
description: The commands plunge adds, its backend URLs and their query keys, environment variables, exit codes, and the Automation API.
prompt: |
  Read https://intentius.io/plunge/reference/ and check this repository's plunge backend URL and environment against it. Report anything it does not list. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Everything else is stock Pulumi's CLI, under the name `plunge`.

## Commands plunge adds

| Command | Effect |
|---|---|
| `plunge stack adopt [--file F] [--json] [--yes] [--stack S]` | Moves a stock stack's checkpoint onto records. `--json` prints [the adopt document](adopt-json/) and never prompts; alone it writes nothing. A refusal exits non-zero (`pkg/cmd/pulumi/stack/stack_adopt.go`) |
| `plunge drift --resource URN [--stream] [--member M] [--stack S]` | One resource's drift from one provider Read, as chant drift record v1 with `--stream` ([Drift of one resource](../how-it-works/drift/)) |
| `plunge version [--pulumi-compatible]` | plunge's version and the Pulumi version it is compatible with |

Refused on a plunge stack by name (`UnsupportedOnRecordsError`):
`plunge stack history`, `plunge stack export --version`,
`plunge stack rm --remove-backups`.

## Backend URLs

| Scheme | Query keys |
|---|---|
| `plunge+file:///path` | none |
| `plunge+s3://bucket/prefix` | `region`, `profile`, `endpoint`, `use_path_style` (or `s3ForcePathStyle`), `bucket_owner`, `allow_insecure` |
| `plunge+gs://bucket/prefix` | `allow_insecure` |
| `plunge+azblob://container/prefix` | `storage_account`, `resource_group`, `subscription_id`, `endpoint`, `metadata_host`, `allow_insecure` |
| `plunge+k8s://namespace/prefix` | `metadata` (required), `kubeconfig`, `context`, `in_cluster`, `estate` |

Source: `pkg/backend/plunge/url.go`, `azblobstore.go`, `azblobmeta.go`, `localstores.go`. A `plunge+gs://` URL reads its
emulator from `STORAGE_EMULATOR_HOST`, which has no query key.

## Environment variables

| Variable | Effect |
|---|---|
| `PLUNGE_REFRESH` | `false` turns off the refresh before `up` and `preview` |
| `PLUNGE_MARKERS` | `false` turns off markers, the bind phase and the delete check. A stack run with it off is outside every claim |
| `PLUNGE_ALLOW_INSECURE` | bucket settings to proceed past, comma separated, as `allow_insecure` |
| `PLUNGE_ALLOW_RANDOM_AUTONAMES` | `true` keeps a random `pulumi:autonaming` pattern; those resources fall outside C1 |
| `PLUNGE_PROGRAM_COMMIT`, `PLUNGE_PROGRAM_TIME` | the program stamp, in place of git's HEAD commit and committer time |
| `PLUNGE_CLOUDCONTROL_ENDPOINT` | where Cloud Control listings go, ahead of the provider's `endpoints` |
| `PLUNGE_AZURE_NATIVE_CACHE` | the directory azure-native's tags table is cached in, or `off` |
| `PLUNGE_AWS_NATIVE_METADATA` | an aws-native `metadata.json` to read in place of the embedded one |
| `AZURE_STORAGE_ACCOUNT`, `AZURE_RESOURCE_GROUP`, `AZURE_SUBSCRIPTION_ID` | `plunge+azblob://` defaults for the query keys |
| `ARM_ACCESS_KEY`, `ARM_SAS_TOKEN`, `ARM_TENANT_ID`, `ARM_CLIENT_ID`, `ARM_CLIENT_SECRET` | `plunge+azblob://` credentials against an emulator, and ARM listings through a `metadataHost` |
| `AZURE_STORAGE_KEY` (with `AZURE_STORAGE_ACCOUNT`), `AZURE_STORAGE_SAS_TOKEN` | `plunge+azblob://` credentials against an emulator: the stack metadata needs one, and the record store takes them after the `ARM_` names |

## Exit codes

Stock Pulumi's codes (`pkg/cmd/pulumi/cmd/exit_codes.go`), plus:

| Code | Meaning |
|---|---|
| 40 | A saved plan's records moved since it was made, or it carries no stamp (`PlanMovedError`, `UnstampedPlanError`). Plan again |

A test fails if an upstream merge adds a code equal to 40.

## Errors plunge names

| Error | When |
|---|---|
| `RecordConflictError` | another run wrote the record first |
| `InFlightDependencyError` | a resource this run would change depends on one with a pending operation |
| `CollisionError` | two or more live objects carry one resource's markers |
| `DeleteRefusedError` | a live object's marker does not name this stack and resource ([#37](https://github.com/INTENTIUS/plunge/issues/37)) |
| `RandomAutonamePatternError` | a random `pulumi:autonaming` pattern on a covered type |
| `MissingReferenceError` | a record refers to a parent, dependency or provider no record holds |
| `ContractRefusedError` | a bucket setting failed or could not be read |
| `MissingMetadataURLError` | a `plunge+k8s://` URL with no `metadata` key |
| `UnsupportedOnRecordsError` | stack history and backups |
| `AdoptionRequiredError` | a run against a stack whose stock checkpoint has resources and no plunge records yet; adopt it first |

## Automation API

The generated wrappers are upstream's; plunge's two commands are hand-written
beside them:

| Language | Where |
|---|---|
| Go | `API.Drift` and `API.StackAdopt` in `sdk/go/auto/automation/plunge_commands.go` |
| Node.js | the `PlungeAPI` class, with `drift` and `stackAdopt`, in `sdk/nodejs/automation/plunge.ts` |
| Python | the `PlungeAPI` class, with `drift` and `stack_adopt`, in `sdk/python/lib/pulumi/automation/plunge.py` |

## Design documents

- [CONCURRENCY.md](https://github.com/INTENTIUS/plunge/blob/main/docs/plunge/CONCURRENCY.md): every race, refusal and listing, in full
- [ADOPT.md](https://github.com/INTENTIUS/plunge/blob/main/docs/plunge/ADOPT.md): the adopt document's contract
- [The corpus](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/README.md): the seven programs and how a baseline is recorded
