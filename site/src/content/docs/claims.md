---
title: Claims and runs
description: The recorded plunge runs, with their dates and files.
prompt: |
  Read https://intentius.io/plunge/claims/. For each recorded run, open its files and check they match the page. Report any difference. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

Each row is a recorded run with its date and files. Open work is tracked in
[GitHub issues](https://github.com/INTENTIUS/plunge/issues).

The first four runs were recorded on 2026-10-26 on the pinned floci, floci-gcp and
floci-az emulators with file backends. `gcp-yaml`'s part of the first run was
re-recorded on 2026-10-27.

| Run | Result | Recording |
|---|---|---|
| Stock parity: `scripts/plunge-baseline.sh` runs `plunge preview` and stock `pulumi preview` (v3.272.0) on the seven corpus programs | `gcp-yaml` matches. The five `aws-*` programs and `azure-yaml` differ only in the names of autonamed resources, where plunge proposes its deterministic name and stock Pulumi a random one | [`tests/plunge-corpus/baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt) and each program's `baseline/plunge-compare.txt`, commit 72050545c0, plunge built from d7653de7f5 ([#3](https://github.com/INTENTIUS/plunge/issues/3)). `gcp-yaml`'s baseline was re-recorded on 2026-10-27 at 67e5270439 with region validation skipped, still `MATCH` ([#42](https://github.com/INTENTIUS/plunge/issues/42)) |
| `plunge up` of `tests/plunge-corpus/aws-ts` on `plunge+file://`, then `plunge stack export` and `plunge stack output --json` | Every taggable resource's inputs carry `plunge-stack` and `plunge-urn`. The bucket policy, which has no tags argument, carries none. Unnamed queues and topics got `<name>-<7 hex>` names. The secret output prints as `"[secret]"` and the export holds its ciphertext | chant [`lexicons/pulumi/src/testdata/plunge-outputs/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-outputs), plunge 30806be26b, floci |
| `plunge drift --resource <urn> --stream` on three resources of the same stack, after a tag was changed through floci's API | One drifted bucket with the changed tag as before and after, one role in sync, one component reported `not-observed` with `unsupported-kind`. Each settle line counts one provider Read (none for the component) and zero stack refreshes | chant [`lexicons/pulumi/src/testdata/plunge-drift/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-drift), plunge 30806be26b, floci |
| `plunge stack adopt --json` on a stock pulumi v3.272.0 stack of `aws-ts` | `would-adopt` (nothing written), `adopted` with `--yes`, `already-adopted` on a re-run, and `refused` with `adopted-from-another-checkpoint` after a stock refresh changed the checkpoint | behold [`src/__fixtures__/plunge-adopt-30806be26b/`](https://github.com/INTENTIUS/behold/tree/main/src/__fixtures__/plunge-adopt-30806be26b), plunge 30806be26b, floci |
| Two SQS queues in one stack on a `plunge+s3://` record store, recorded on 2026-10-27 by `scripts/plunge-races/races.sh lifecycle`: preview, up, two concurrent `plunge up` runs each changing one queue, `stack export`, a run of an older program, `plunge drift --resource` on each queue, destroy | Both concurrent updates land, and no lock object appears in the store. The export passes integrity checking, holds both changes and matches one record per URN. The older program's run keeps the queue it no longer declares and warns with both program stamps. Drift reports the retagged queue as drifted and the other in sync, each with one provider Read and zero stack refreshes. Destroy removes both queues and their records | [`tests/plunge-races/lifecycle/`](https://github.com/INTENTIUS/plunge/tree/main/tests/plunge-races/lifecycle), plunge built from f6dae0ff5d, floci ([#6](https://github.com/INTENTIUS/plunge/issues/6)) |
