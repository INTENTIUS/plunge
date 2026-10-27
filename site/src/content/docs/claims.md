---
title: Claims and runs
description: The four recorded plunge runs, with their dates and files, and every capability that waits for a run, with its issue.
prompt: |
  Read https://intentius.io/plunge/claims/. For each recorded run, open its files and check they match the page. Report any difference. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Under choudoufu's rule, which plunge follows, a promise with no proof is a GitHub issue.
Every claim here rests on a run below, a real CLI run with its output
committed. A capability with unit tests and no recorded run sits in the
tables below with its issue.

## Recorded runs

All four ran on 2026-10-26 on local emulators (choudoufu's pinned floci,
floci-gcp and floci-az images) with file backends and fake credentials, and
none touched a real cloud.

| Run | Result | Recording |
|---|---|---|
| Stock parity: `scripts/plunge-baseline.sh` runs `plunge preview` and stock `pulumi preview` (v3.272.0) on the seven corpus programs | `gcp-yaml` matches. The five `aws-*` programs and `azure-yaml` differ only in the names of autonamed resources, where plunge proposes its deterministic name and stock Pulumi a random one | [`tests/plunge-corpus/baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt) and each program's `baseline/plunge-compare.txt`, commit 72050545c0, plunge built from d7653de7f5 ([#3](https://github.com/INTENTIUS/plunge/issues/3)). `gcp-yaml`'s baseline was re-recorded on 2026-10-27 at 67e5270439 with region validation skipped, still `MATCH` ([#42](https://github.com/INTENTIUS/plunge/issues/42)) |
| `plunge up` of `tests/plunge-corpus/aws-ts` on `plunge+file://`, then `plunge stack export` and `plunge stack output --json` | Every taggable resource's inputs carry `plunge-stack` and `plunge-urn`. The bucket policy, which has no tags argument, carries none. Unnamed queues and topics got `<name>-<7 hex>` names. The secret output prints as `"[secret]"` and the export holds its ciphertext | chant [`lexicons/pulumi/src/testdata/plunge-outputs/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-outputs), plunge 30806be26b, floci |
| `plunge drift --resource <urn> --stream` on three resources of the same stack, after a tag was changed through floci's API | One drifted bucket with the changed tag as before and after, one role in sync, one component reported `not-observed` with `unsupported-kind`. Each settle line counts one provider Read (none for the component) and zero stack refreshes | chant [`lexicons/pulumi/src/testdata/plunge-drift/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-drift), plunge 30806be26b, floci |
| `plunge stack adopt --json` on a stock pulumi v3.272.0 stack of `aws-ts` | `would-adopt` (nothing written), `adopted` with `--yes`, `already-adopted` on a re-run, and `refused` with `adopted-from-another-checkpoint` after a stock refresh changed the checkpoint | behold [`src/__fixtures__/plunge-adopt-30806be26b/`](https://github.com/INTENTIUS/behold/tree/main/src/__fixtures__/plunge-adopt-30806be26b), plunge 30806be26b, floci |

Each AWS run is on floci, so it shows plunge's behaviour on an AWS emulator
and none on AWS itself.

## Built, waiting for a run

Each row is in the code on main. "Unit-tested" means its tests passed in the
Go suites, last on 2026-10-27 at d168d5cb7b (pkg/backend/plunge and
./engine/... among them), with fakes only.

| Capability | State | Run held by |
|---|---|---|
| Two runs on different resources of one stack both land, with no stack lock | Unit-tested (`TestTwoConcurrentUpdatesOnDisjointResourcesBothLand`) | [#6](https://github.com/INTENTIUS/plunge/issues/6) |
| The record store on a real S3 or GCS bucket, and on floci, floci-gcp and floci-az | choudoufu's conformance suite passed on scratch emulators for `file`, `gs` and `azblob`, and 16 of 18 for `s3` (floci ignores `If-Match` on `DeleteObject`, [lex00/floci#263](https://github.com/lex00/floci/issues/263)). `scripts/plunge-record-store-acceptance.sh` sets the bucket settings ([#34](https://github.com/INTENTIUS/plunge/issues/34)) and has not run | [#6](https://github.com/INTENTIUS/plunge/issues/6), [#14](https://github.com/INTENTIUS/plunge/issues/14), [#34](https://github.com/INTENTIUS/plunge/issues/34) |
| Adopting a corpus stack, the first `plunge up` after it, and backing out to stock | Unit-tested (`TestAdoptionIsGated`, `TestAdoptionIsRecordedAndReRunIsANoOp`, `TestBackOutLeavesOnlyMarkerTagRemovals`) | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| Adopting indexed cloud objects with `plunge stack adopt --adopt` | Unit-tested (`adoptindex_test.go`, `stack_adopt_test.go`) | [#52](https://github.com/INTENTIUS/plunge/issues/52) |
| Races C1 to C6 (racing creates, two writers on one record, update racing a destroy, a crash is a re-run, a moved saved plan) | Unit-tested; `scripts/plunge-races/races.sh` written | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| C1: the loser of a racing create binds the winner on its re-run, pulumi-aws and aws-native | Unit-tested (`TestRacingCreateLoserBindsTheWinnerOnReRun`); the aws-native race `c1-native` is in `races.sh` ([#36](https://github.com/INTENTIUS/plunge/issues/36), closed) and has not run | [#10](https://github.com/INTENTIUS/plunge/issues/10) |
| ARM listing consistency on a real subscription | Not measured | [#15](https://github.com/INTENTIUS/plunge/issues/15) |
| The azure-yaml corpus program | Its stock baseline is in the 2026-10-26 re-record above. A clean `plunge up` and destroy on floci-az ran on 2026-10-27, unrecorded | [#16](https://github.com/INTENTIUS/plunge/issues/16) |
| The baseline after an upstream merge | Script written | [#3](https://github.com/INTENTIUS/plunge/issues/3) |
| floci-gcp's Cloud SQL, GKE and Assured Workloads list paths | Not measured | [#32](https://github.com/INTENTIUS/plunge/issues/32) |
| `plunge graph --json`, a stack's graph from its records, loading in behold | Unit-tested (`graph_test.go`) | [#40](https://github.com/INTENTIUS/plunge/issues/40) |
| `import:` refused for another stack's marker or create-only tags, and the delete half of a replace checked like any delete | Unit-tested (`importvet_test.go`, `plunge_importvet_test.go`, `plunge_deletevet_test.go`); an unmarked import waits for a ruling | [#51](https://github.com/INTENTIUS/plunge/issues/51) |
| A gate on the markers: an IAM condition, an admission policy | Not written | [#45](https://github.com/INTENTIUS/plunge/issues/45) |
| Rename by `aliases`, `plunge state delete` on records, recovering a lost store, StackReference to a plunge stack | Not written | [#46](https://github.com/INTENTIUS/plunge/issues/46), [#47](https://github.com/INTENTIUS/plunge/issues/47), [#48](https://github.com/INTENTIUS/plunge/issues/48), [#49](https://github.com/INTENTIUS/plunge/issues/49) |

## Tested, closed, not recorded

These issues closed on 2026-10-27 with their tests green in the Go suites.
None has a recorded run, so none is a claim here; plunge adds a claim only
when the conformance suite and the corpus runs cannot cover it
([#44](https://github.com/INTENTIUS/plunge/issues/44)).

| Capability | Tests | Issue |
|---|---|---|
| The sweep matches recorded Lambda functions and queues by their other names, so it stops warning about them | `TestSweepMatchesRecordsByTheirOtherNames`; a clean `up` and destroy of each corpus program on floci showed no sweep warning | [#28](https://github.com/INTENTIUS/plunge/issues/28) |
| `aws_budgets_budget_action` listed with the account ID from the stack's own STS | `accountid_test.go` | [#31](https://github.com/INTENTIUS/plunge/issues/31) |
| Every delete checked against the live marker, `destroy` included | `deletevet_test.go`, `plunge_deletevet_test.go`; the corpus destroys on floci were refused on SSM parameters, whose tags floci drops ([lex00/floci#263](https://github.com/lex00/floci/issues/263)) | [#37](https://github.com/INTENTIUS/plunge/issues/37) |
| ARM listings through the azure provider's `metadataHost` | `TestARMListingGoesThroughTheMetadataHost`; azure-yaml's `up` and destroy on floci-az made no real-cloud call | [#41](https://github.com/INTENTIUS/plunge/issues/41) |
| GCP listings fetch no token from Google when the stack names local endpoints | `TestGCPListersFetchNoTokenFromANonLocalHost` | [#42](https://github.com/INTENTIUS/plunge/issues/42) |
| S3, GCS and Azure Blob stores, and their stack metadata, never build a default credential chain against an emulator | `localstores_test.go`, `azblobmeta_test.go` | [#50](https://github.com/INTENTIUS/plunge/issues/50) |

## Release

plunge `v0.1.0` (Pulumi-compatible `3.272.0`) was published on 2026-10-27 from
main 23ce7e7e24 by the `plunge-v*` tag workflow
([release](https://github.com/INTENTIUS/plunge/releases/tag/plunge-v0.1.0),
[#13](https://github.com/INTENTIUS/plunge/issues/13),
[#33](https://github.com/INTENTIUS/plunge/issues/33)).

## Research and later work

[#38](https://github.com/INTENTIUS/plunge/issues/38) (closed) asked whether
records should hold less than the full state, and recommends keeping them
full. [#39](https://github.com/INTENTIUS/plunge/issues/39) moved plunge onto
choudoufu's public packages and stays open until the `s3` conformance leg
passes.
