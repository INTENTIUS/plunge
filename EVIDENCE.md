# Evidence

What plunge's recorded runs show, how each was checked, and what has no run yet. The docs site does not carry these. This file is updated with each release.

This file is current as of plunge v0.2.0, released 2026-10-29. Each row is a recorded run with its date and files. Open work is tracked in
[GitHub issues](https://github.com/INTENTIUS/plunge/issues).

## How runs are checked

Every run used floci, floci-gcp or floci-az, the local AWS, Google Cloud and Azure emulators, and none touched a real cloud. `scripts/plunge-safe-run.sh` guards that. A recorded run keeps its files in this repository, in chant or in behold, and the row links them. To check a row, open its files and compare them with the Result column.

The first four runs below were recorded on 2026-10-26 on the pinned floci, floci-gcp and floci-az emulators with file backends. `gcp-yaml`'s part of the first run was re-recorded on 2026-10-27.

## Recorded runs

| Run | Result | Recording |
|---|---|---|
| Stock parity: `scripts/plunge-baseline.sh` runs `plunge preview` and stock `pulumi preview` (v3.272.0) on the seven corpus programs | `gcp-yaml` matches. The five `aws-*` programs and `azure-yaml` differ only in the names of autonamed resources, where plunge proposes its deterministic name and stock Pulumi a random one | [`tests/plunge-corpus/baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt) and each program's `baseline/plunge-compare.txt`, commit 72050545c0, plunge built from d7653de7f5 ([#3](https://github.com/INTENTIUS/plunge/issues/3)). `gcp-yaml`'s baseline was re-recorded on 2026-10-27 at 67e5270439 with region validation skipped, still `MATCH` ([#42](https://github.com/INTENTIUS/plunge/issues/42)) |
| `plunge up` of `tests/plunge-corpus/aws-ts` on `plunge+file://`, then `plunge stack export` and `plunge stack output --json` | Every taggable resource's inputs carry `plunge-stack` and `plunge-urn`. The bucket policy, which has no tags argument, carries none. Unnamed queues and topics got `<name>-<7 hex>` names. The secret output prints as `"[secret]"` and the export holds its ciphertext | chant [`lexicons/pulumi/src/testdata/plunge-outputs/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-outputs), plunge 30806be26b, floci |
| `plunge drift --resource <urn> --stream` on three resources of the same stack, after a tag was changed through floci's API | One drifted bucket with the changed tag as before and after, one role in sync, one component reported `not-observed` with `unsupported-kind`. Each settle line counts one provider Read (none for the component) and zero stack refreshes | chant [`lexicons/pulumi/src/testdata/plunge-drift/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-drift), plunge 30806be26b, floci |
| `plunge stack adopt --json` on a stock pulumi v3.272.0 stack of `aws-ts` | `would-adopt` (nothing written), `adopted` with `--yes`, `already-adopted` on a re-run, and `refused` with `adopted-from-another-checkpoint` after a stock refresh changed the checkpoint | behold [`src/__fixtures__/plunge-adopt-30806be26b/`](https://github.com/INTENTIUS/behold/tree/main/src/__fixtures__/plunge-adopt-30806be26b), plunge 30806be26b, floci |
| Two SQS queues in one stack on a `plunge+s3://` record store, recorded on 2026-10-28 by `scripts/plunge-races/races.sh lifecycle`: preview, up, two concurrent `plunge up` runs each changing one queue, `stack export`, a run of an older program, `plunge drift --resource` on each queue, destroy | Both concurrent updates land, and no lock object appears in the store. The export passes integrity checking, holds both changes and matches one record per URN. The older program's run keeps the queue it no longer declares and warns with both program stamps. Drift reports the retagged queue as drifted and the other in sync, each with one provider Read and zero stack refreshes. Destroy removes both queues and their records | [`tests/plunge-races/lifecycle/`](https://github.com/INTENTIUS/plunge/tree/main/tests/plunge-races/lifecycle), plunge built from ab9158fdf0, floci ([#6](https://github.com/INTENTIUS/plunge/issues/6)) |
| The concurrency races on `plunge+s3://` record stores, recorded on 2026-10-28 by `scripts/plunge-races/races.sh`: two first runs of one client-named bucket and role (`c1`), two first runs of one VPC (`c2`), a saved plan applied after its resource moved, with an unmoved control (`c3`), a destroy racing an update (`c4`), two runs changing different queues (`c5-disjoint`) and the same queue (`c5-same`, then under SSE-S3 and SSE-KMS bucket encryption), and a run killed with `kill -9` part way (`c6`) | One bucket, one role and one VPC after the racing first runs, each bound by its markers, and both checkouts plan no changes. The moved plan is refused with exit 40 naming the queue, and the unmoved one applies. The losing destroy is told by name and its re-run holds. Disjoint changes both land. On the same queue one write lands and the other run is refused by name, under each encryption setting, with no lock object in the store at any point. The re-run after the kill finishes with no cancel or unlock and leaves each queue once | [`tests/plunge-races/`](https://github.com/INTENTIUS/plunge/tree/main/tests/plunge-races) (`c1` to `c6`), plunge built from ab9158fdf0, floci ([#7](https://github.com/INTENTIUS/plunge/issues/7)) |

## By platform

| Platform | Recorded | How it was checked |
|---|---|---|
| AWS | Stock parity on `aws-ts`, `aws-python`, `aws-go`, `aws-yaml` and `aws-csharp`: the same steps, differing only in the names of autonamed resources. `plunge up` of `aws-ts` on `plunge+file://`, with every taggable resource marked and the bucket policy unmarked. `plunge drift --resource --stream`. `plunge stack adopt --json` | `scripts/plunge-baseline.sh`, `tests/plunge-corpus/baseline-verdicts.txt`, chant `plunge-outputs/` and `plunge-drift/`, behold `plunge-adopt-30806be26b/` fixtures |
| Azure | `plunge preview` against stock `pulumi preview` on `azure-yaml`, on floci-az, 2026-10-26: `DIFF-INPUTS`, where only the autonamed resource group `scratch-rg` differs | `tests/plunge-corpus/baseline-verdicts.txt` |
| GCP | `plunge preview` against stock `pulumi preview` on `gcp-yaml`, on floci-gcp, 2026-10-26: `MATCH` | `tests/plunge-corpus/baseline-verdicts.txt` |
| Kubernetes | No recorded run | |

## Other recorded results

- Adopt, 2026-10-26: a stock pulumi v3.272.0 `aws-ts` stack on floci gave `would-adopt`, `adopted`, `already-adopted`, and `refused` with `adopted-from-another-checkpoint`, and with no provider schema loaded every resource was `pending` (behold's [fixture](https://github.com/INTENTIUS/behold/tree/main/src/__fixtures__/plunge-adopt-30806be26b)). A stock refresh changed the checkpoint before the refusal.
- Names, identity and records: in the `aws-ts` run, queue `events` became `events-76012a7`, topic `docs-notify` became `docs-notify-313a6f6`, and bucket `docs-assets` (a child of component `docs`) carried `plunge-stack = corpus-aws-ts/floci` and `plunge-urn = corpus:index:Site$aws:s3/bucket:Bucket::docs-assets`. The local store ran in these runs.
- Tutorial: two of the recorded runs came from a run like the site's tutorial. A clean `up` and destroy of each corpus program on floci on 2026-10-27 printed no warning of a marked object with no record ([#28](https://github.com/INTENTIUS/plunge/issues/28)).

## Not recorded

- The delete check, which compares an object's live marker with the stack and resource before every delete, `plunge destroy` included. Its tests passed on 2026-10-27 and [#37](https://github.com/INTENTIUS/plunge/issues/37) is closed, but no run of it is recorded.
- A gate policy (an IAM, Azure Policy or GCP condition on plunge's markers). No policy has been run against plunge's markers on floci or AWS, on Azure or on GCP; [#45](https://github.com/INTENTIUS/plunge/issues/45) holds that run. Until it lands, the site shows the policy's shape and claims no fencing of plunge's writes.
- A run with `PLUNGE_MARKERS=false` is outside every claim.
- Kubernetes has no recorded run.

<!-- product-scenario rows: written by `node scripts/plunge-evidence/claims.mjs --write` from scripts/plunge-evidence/claims.json -->

## Product scenario rows

A claim can cite a row of a product's scenario. The table below gives each cited row its repo and scenario, the product commit and date, and the plunge version it ran. It counts only at the version the claim is about. At any other version it is listed and marked not counted. It never replaces the runs in the table above.

No row is recorded yet.

Pending citations have no recorded run until the scenario has a row.

| Claim | Repo | Scenario | To record |
|---|---|---|---|
| `c5-disjoint` | INTENTIUS/plywood | `pw-disjoint` | disjoint updates, markers and plungeTimings (plunge#55) |
| `c3` | INTENTIUS/plywood | `pw-moved-plan` | a saved plan applied after its resource moved, refused with exit 40 |
| `adopt` | INTENTIUS/plywood | `pw-adopt` | adopting a stock stack |

<!-- end product-scenario rows -->
