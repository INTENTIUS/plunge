---
title: Claims and runs
description: The four recorded plunge runs, with their dates and files, and every capability that waits for a run, with its issue.
prompt: |
  Read https://intentius.io/plunge/claims/. For each recorded run, open its files and check they say what the page says. Report any difference. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

plunge follows choudoufu's rule: a promise with no proof is a GitHub issue,
never a claim. A claim on this site is backed by one of the runs below, each a
real CLI run with its output committed. Unit tests are code evidence and are
named where they exist, but a capability with tests and no run is listed in
the second table, with the issue that holds its run.

## Recorded runs

All four ran on 2026-10-26 against local emulators (choudoufu's pinned floci,
floci-gcp and floci-az images), with file backends and fake credentials. None
touched a real cloud.

| Run | What it shows | Where the recording is |
|---|---|---|
| Stock parity: `scripts/plunge-baseline.sh` runs `plunge preview` and stock `pulumi preview` (v3.272.0) on the seven corpus programs | `gcp-yaml` matches. The five `aws-*` programs and `azure-yaml` differ only in the names of autonamed resources, where plunge proposes its deterministic name and stock Pulumi a random one | [`tests/plunge-corpus/baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt) and each program's `baseline/plunge-compare.txt`, commit 72050545c0, plunge built from d7653de7f5 ([#3](https://github.com/INTENTIUS/plunge/issues/3)) |
| `plunge up` of `tests/plunge-corpus/aws-ts` on `plunge+file://`, then `plunge stack export` and `plunge stack output --json` | Every taggable resource's inputs carry `plunge-stack` and `plunge-urn`. The bucket policy, which has no tags argument, carries none. Unnamed queues and topics got `<name>-<7 hex>` names. The secret output prints as `"[secret]"` and the export holds its ciphertext | chant [`lexicons/pulumi/src/testdata/plunge-outputs/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-outputs), plunge 30806be26b, floci |
| `plunge drift --resource <urn> --stream` on three resources of the same stack, after a tag was changed through floci's API | One drifted bucket with the changed tag as before and after, one role in sync, one component reported `not-observed` with `unsupported-kind`. Each settle line counts one provider Read (none for the component) and zero stack refreshes | chant [`lexicons/pulumi/src/testdata/plunge-drift/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-drift), plunge 30806be26b, floci |
| `plunge stack adopt --json` on a stock pulumi v3.272.0 stack of `aws-ts` | `would-adopt` (nothing written), `adopted` with `--yes`, `already-adopted` on a re-run, and `refused` with `adopted-from-another-checkpoint` after a stock refresh changed the checkpoint | behold [`src/__fixtures__/plunge-adopt-30806be26b/`](https://github.com/INTENTIUS/behold/tree/main/src/__fixtures__/plunge-adopt-30806be26b), plunge 30806be26b, floci |

Each AWS run is on floci, so it shows plunge's behaviour against an emulator
of AWS, not against AWS itself.

## Built, waiting for a run

Each row is in the code. "Unit-tested" means its tests ran green when the
engine tier shipped (main 61f622536b). "Tests written" means the tests exist on
the integration branch and have not run yet.

| Capability | State | Run held by |
|---|---|---|
| Two runs on different resources of one stack both land, with no stack lock | Unit-tested (`TestTwoConcurrentUpdatesOnDisjointResourcesBothLand`) | [#6](https://github.com/INTENTIUS/plunge/issues/6) |
| The record store on a real S3 or GCS bucket, and on floci, floci-gcp and floci-az | Scripts written (`scripts/plunge-record-store-acceptance.sh`), bucket settings fixed in [#34](https://github.com/INTENTIUS/plunge/issues/34) | [#6](https://github.com/INTENTIUS/plunge/issues/6), [#14](https://github.com/INTENTIUS/plunge/issues/14) |
| Adopting a corpus stack, the first `plunge up` after it, and backing out to stock | Unit-tested (`TestAdoptionIsGated`, `TestAdoptionIsRecordedAndReRunIsANoOp`, `TestBackOutLeavesOnlyMarkerTagRemovals`) | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| Races C1 to C6 (racing creates, two writers on one record, update racing a destroy, a crash is a re-run, a moved saved plan) | Unit-tested; `scripts/plunge-races/races.sh` written | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| C1: the loser of a racing create binds the winner on its re-run, pulumi-aws and aws-native | Unit-tested (`TestRacingCreateLoserBindsTheWinnerOnReRun`); the aws-native race is written | [#10](https://github.com/INTENTIUS/plunge/issues/10), [#36](https://github.com/INTENTIUS/plunge/issues/36) |
| ARM listing consistency on a real subscription | Not measured | [#15](https://github.com/INTENTIUS/plunge/issues/15) |
| The azure-yaml corpus program | Its stock baseline is in the 2026-10-26 re-record above; nothing has been applied | [#16](https://github.com/INTENTIUS/plunge/issues/16) |
| The baseline after an upstream merge | Script written | [#3](https://github.com/INTENTIUS/plunge/issues/3) |
| Pulumi-aws ids for single-part Cloud Control identifiers, so the sweep stops warning about recorded Lambda functions and queues | Tests written | [#28](https://github.com/INTENTIUS/plunge/issues/28) |
| `aws_budgets_budget_action` listed with the account ID from the stack's own STS | Tests written | [#31](https://github.com/INTENTIUS/plunge/issues/31) |
| floci-gcp's Cloud SQL, GKE and Assured Workloads list paths | Not measured | [#32](https://github.com/INTENTIUS/plunge/issues/32) |
| Every delete checked against the live marker, `destroy` included | Tests written | [#37](https://github.com/INTENTIUS/plunge/issues/37) |
| ARM listings through the azure provider's `metadataHost` | Tests written | [#41](https://github.com/INTENTIUS/plunge/issues/41) |
| GCP listings fetch no token from Google when the stack names local endpoints | Tests written | [#42](https://github.com/INTENTIUS/plunge/issues/42) |
| A gate on the markers: an IAM condition, an admission policy | Not written | [#45](https://github.com/INTENTIUS/plunge/issues/45) |
| Rename by `aliases`, `plunge state delete` on records, recovering a lost store, StackReference to a plunge stack | Not written | [#46](https://github.com/INTENTIUS/plunge/issues/46), [#47](https://github.com/INTENTIUS/plunge/issues/47), [#48](https://github.com/INTENTIUS/plunge/issues/48), [#49](https://github.com/INTENTIUS/plunge/issues/49) |
| A release | The `plunge-v*` tag workflow is written | [#13](https://github.com/INTENTIUS/plunge/issues/13), [#33](https://github.com/INTENTIUS/plunge/issues/33) |

## Research and later work

[#38](https://github.com/INTENTIUS/plunge/issues/38) asks whether records
should hold less than the full state; its report recommends keeping them full.
[#39](https://github.com/INTENTIUS/plunge/issues/39) moves plunge onto
choudoufu's public packages, and
[#40](https://github.com/INTENTIUS/plunge/issues/40) emits a stack's graph from
its records.
