---
title: Claims and runs
description: Each plunge capability with its status (real cloud, emulator, unit-tested or not built), then the four recorded runs with their dates and files.
prompt: |
  Read https://intentius.io/plunge/claims/. For each recorded run, open its files and check they match the page. Report any difference. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Each capability below has one of four statuses. **Real cloud** means a
recorded run on AWS, Google Cloud, Azure or a real cluster; nothing has one
yet. **Emulator** means a recorded run on a local emulator such as
[floci](../glossary/#floci). **Unit-tested** means its Go tests pass, with
fakes in place of the cloud. **Not built** means the code is not written.

| Capability | Status | Link |
|---|---|---|
| `plunge preview` plans the same steps as stock `pulumi preview` on seven corpus programs, apart from autonamed names | Emulator[^parity] | [Recorded runs](#recorded-runs), [#3](https://github.com/INTENTIUS/plunge/issues/3) |
| `plunge up` writes the markers and fixed names, and `stack export` and `stack output` read the result | Emulator | [Recorded runs](#recorded-runs) |
| `plunge drift --resource` reads one resource's drift with one provider Read | Emulator | [Drift of one resource](../how-it-works/drift/) |
| `plunge stack adopt` classifies, adopts, re-runs as a no-op and refuses a changed checkpoint | Emulator | [Adopt an existing stack](../use/migrate/) |
| Two runs on different resources of one stack both land, with no stack lock | Unit-tested[^tests] | [#6](https://github.com/INTENTIUS/plunge/issues/6) |
| The S3, GCS, Azure Blob and Kubernetes record stores | Unit-tested[^stores] | [#6](https://github.com/INTENTIUS/plunge/issues/6), [#14](https://github.com/INTENTIUS/plunge/issues/14), [#34](https://github.com/INTENTIUS/plunge/issues/34) |
| Adopting a corpus stack end to end, the first `plunge up` after it, and backing out to stock | Unit-tested[^tests] | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| Adopting indexed cloud objects with `plunge stack adopt --adopt` | Unit-tested[^tests] | [#52](https://github.com/INTENTIUS/plunge/issues/52) |
| C1: the loser of a racing create binds the winner on its re-run, on pulumi-aws and aws-native | Unit-tested[^tests] | [#10](https://github.com/INTENTIUS/plunge/issues/10), [#36](https://github.com/INTENTIUS/plunge/issues/36) |
| The races C1 to C6 on floci | Unit-tested; the race script is written and has not run | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| A saved plan whose records moved is refused, exit 40 | Unit-tested[^tests] | [Saved plans](../how-it-works/concurrency/#saved-plans) |
| Every delete, `destroy` included, is checked against the live marker | Unit-tested[^closed] | [#37](https://github.com/INTENTIUS/plunge/issues/37) |
| The sweep matches recorded Lambda functions and queues by their other names | Unit-tested[^closed] | [#28](https://github.com/INTENTIUS/plunge/issues/28) |
| `aws_budgets_budget_action` is listed with the account ID from the stack's own STS | Unit-tested[^closed] | [#31](https://github.com/INTENTIUS/plunge/issues/31) |
| ARM listings go through the azure provider's `metadataHost` | Unit-tested[^closed] | [#41](https://github.com/INTENTIUS/plunge/issues/41) |
| GCP listings fetch no token from Google when the stack names local endpoints | Unit-tested[^closed] | [#42](https://github.com/INTENTIUS/plunge/issues/42) |
| Stores pointed at an emulator never build a default credential chain | Unit-tested[^closed] | [#50](https://github.com/INTENTIUS/plunge/issues/50) |
| `import:` refused for another stack's marker or for create-only tags | Unit-tested[^tests]; an unmarked import waits for a ruling | [#51](https://github.com/INTENTIUS/plunge/issues/51) |
| `plunge graph --json` loads a stack's graph in behold | Unit-tested[^tests] | [#40](https://github.com/INTENTIUS/plunge/issues/40) |
| The azure-yaml corpus program under `plunge up` and destroy | One clean run on floci-az, unrecorded | [#16](https://github.com/INTENTIUS/plunge/issues/16) |
| The baseline after an upstream merge | Not run; the script is written | [#3](https://github.com/INTENTIUS/plunge/issues/3) |
| ARM listing consistency on a real subscription | Not measured | [#15](https://github.com/INTENTIUS/plunge/issues/15) |
| floci-gcp's Cloud SQL, GKE and Assured Workloads list paths | Not measured | [#32](https://github.com/INTENTIUS/plunge/issues/32) |
| A gate on the markers: an IAM condition or an admission policy | Not built | [#45](https://github.com/INTENTIUS/plunge/issues/45) |
| Rename by `aliases`, `plunge state delete` on records, rebuilding a lost store, StackReference to a plunge stack | Not built | [#46](https://github.com/INTENTIUS/plunge/issues/46), [#47](https://github.com/INTENTIUS/plunge/issues/47), [#48](https://github.com/INTENTIUS/plunge/issues/48), [#49](https://github.com/INTENTIUS/plunge/issues/49) |

C1 and C2 are the two claims about creates. In C1, two runs create the same
resource, its fixed name makes the cloud refuse the second, and the loser's
next run binds the winner's object. In C2, plunge lists live objects carrying
a resource's markers before it creates it, binds one and refuses two. C3 to
C6 are the races in the [glossary](../glossary/#c1-to-c6).

A capability becomes a claim when a run of it is recorded. Most behaviour is
covered by the shared store conformance suite and the emulator corpus runs,
so a new recorded claim needs a reason those two cannot cover it
([#44](https://github.com/INTENTIUS/plunge/issues/44)).

[^parity]: `gcp-yaml` matches. The five `aws-*` programs and `azure-yaml` differ only in the names of autonamed resources.
[^tests]: The Go suites passed on 2026-10-27 at d168d5cb7b, with fakes only (`pkg/backend/plunge` and `./engine/...` among them). The tests: `TestTwoConcurrentUpdatesOnDisjointResourcesBothLand`; `TestAdoptionIsGated`, `TestAdoptionIsRecordedAndReRunIsANoOp`, `TestBackOutLeavesOnlyMarkerTagRemovals`; `adoptindex_test.go`, `stack_adopt_test.go`; `TestRacingCreateLoserBindsTheWinnerOnReRun`; `TestMovedPlanIsRefusedWithExitCode40`; `importvet_test.go`, `plunge_importvet_test.go`, `plunge_deletevet_test.go`; `graph_test.go`.
[^stores]: choudoufu's store conformance suite passed on scratch emulators on 2026-10-27 for `file`, `gs` and `azblob`, and 16 of 18 for `s3`, since floci ignores `If-Match` on `DeleteObject` ([lex00/floci#263](https://github.com/lex00/floci/issues/263)). None of it is recorded. `scripts/plunge-record-store-acceptance.sh` sets the bucket settings and has not run.
[^closed]: These issues closed on 2026-10-27 with their tests green: `deletevet_test.go` and `plunge_deletevet_test.go` (#37), `TestSweepMatchesRecordsByTheirOtherNames` (#28), `accountid_test.go` (#31), `TestARMListingGoesThroughTheMetadataHost` (#41), `TestGCPListersFetchNoTokenFromANonLocalHost` (#42), `localstores_test.go` and `azblobmeta_test.go` (#50). A clean `up` and destroy of each corpus program on floci showed no sweep warning; each `aws-*` destroy was refused on SSM parameters, whose tags floci drops.

## Recorded runs

The four runs were recorded on 2026-10-26 on local emulators (choudoufu's
pinned floci, floci-gcp and floci-az images), with file backends and fake
credentials. None touched a real cloud. `gcp-yaml`'s part of the first run was
re-recorded on 2026-10-27, as its row says.

| Run | Result | Recording |
|---|---|---|
| Stock parity: `scripts/plunge-baseline.sh` runs `plunge preview` and stock `pulumi preview` (v3.272.0) on the seven corpus programs | `gcp-yaml` matches. The five `aws-*` programs and `azure-yaml` differ only in the names of autonamed resources, where plunge proposes its deterministic name and stock Pulumi a random one | [`tests/plunge-corpus/baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt) and each program's `baseline/plunge-compare.txt`, commit 72050545c0, plunge built from d7653de7f5 ([#3](https://github.com/INTENTIUS/plunge/issues/3)). `gcp-yaml`'s baseline was re-recorded on 2026-10-27 at 67e5270439 with region validation skipped, still `MATCH` ([#42](https://github.com/INTENTIUS/plunge/issues/42)) |
| `plunge up` of `tests/plunge-corpus/aws-ts` on `plunge+file://`, then `plunge stack export` and `plunge stack output --json` | Every taggable resource's inputs carry `plunge-stack` and `plunge-urn`. The bucket policy, which has no tags argument, carries none. Unnamed queues and topics got `<name>-<7 hex>` names. The secret output prints as `"[secret]"` and the export holds its ciphertext | chant [`lexicons/pulumi/src/testdata/plunge-outputs/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-outputs), plunge 30806be26b, floci |
| `plunge drift --resource <urn> --stream` on three resources of the same stack, after a tag was changed through floci's API | One drifted bucket with the changed tag as before and after, one role in sync, one component reported `not-observed` with `unsupported-kind`. Each settle line counts one provider Read (none for the component) and zero stack refreshes | chant [`lexicons/pulumi/src/testdata/plunge-drift/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-drift), plunge 30806be26b, floci |
| `plunge stack adopt --json` on a stock pulumi v3.272.0 stack of `aws-ts` | `would-adopt` (nothing written), `adopted` with `--yes`, `already-adopted` on a re-run, and `refused` with `adopted-from-another-checkpoint` after a stock refresh changed the checkpoint | behold [`src/__fixtures__/plunge-adopt-30806be26b/`](https://github.com/INTENTIUS/behold/tree/main/src/__fixtures__/plunge-adopt-30806be26b), plunge 30806be26b, floci |

Each AWS run is on floci, so it shows plunge's behaviour on an AWS emulator
and none on AWS itself.

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
