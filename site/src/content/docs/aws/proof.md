---
title: Proof
description: What has run on AWS, with dates and recordings, and what waits for a run.
prompt: |
  Follow https://intentius.io/plunge/aws/proof/. Open each recording it links and check the page's description of it. Report any difference. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Recorded runs

Every recorded plunge run so far is on floci, the AWS emulator, on 2026-10-26
([Claims and runs](/plunge/claims/)):

- `plunge preview` against stock `pulumi preview` on `aws-ts`, `aws-python`, `aws-go`, `aws-yaml` and `aws-csharp`: the same steps, differing only in the names of autonamed resources ([`baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt)).
- `plunge up` of `aws-ts` on `plunge+file://`, then `stack export` and `stack output`: markers on every taggable resource, none on the bucket policy, deterministic names on the queues and the topic (chant [`plunge-outputs/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-outputs)).
- `plunge drift --resource --stream`: one drifted bucket, one role in sync, one component not observed, one provider Read each (chant [`plunge-drift/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-drift)).
- `plunge stack adopt --json`: would-adopt, adopted, already-adopted, refused (behold [fixture](https://github.com/INTENTIUS/behold/tree/main/src/__fixtures__/plunge-adopt-30806be26b)).

Each shows behaviour against an emulator, not against AWS.

## Waiting for a run

| What | Issue |
|---|---|
| The S3 record store on floci, and two updates on different resources of one stack | [#6](https://github.com/INTENTIUS/plunge/issues/6) |
| Adopting a corpus stack end to end, the first update, the back-out, and races C1 to C6 | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| C1 races on pulumi-aws and aws-native | [#10](https://github.com/INTENTIUS/plunge/issues/10), [#36](https://github.com/INTENTIUS/plunge/issues/36) |
| Sweep warnings for recorded Lambda functions and queues | [#28](https://github.com/INTENTIUS/plunge/issues/28) |
| Budgets actions listed with the account ID | [#31](https://github.com/INTENTIUS/plunge/issues/31) |
| The delete check against the live tags | [#37](https://github.com/INTENTIUS/plunge/issues/37) |
| An IAM gate on `plunge-stack` | [#45](https://github.com/INTENTIUS/plunge/issues/45) |

## Run one now

The [Tutorial](/plunge/use/tutorial/) repeats the `aws-ts` run on floci.
