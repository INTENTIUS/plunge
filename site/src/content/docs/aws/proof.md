---
title: Proof
description: Recorded runs on AWS, with dates and recordings, and the runs still to come.
prompt: |
  Follow https://intentius.io/plunge/aws/proof/. Open each recording it links and check the page's description of it. Report any difference. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Recorded runs

Every recorded run so far ran on 2026-10-26 on floci, the AWS emulator
([Claims and runs](/plunge/claims/)):

- `plunge preview` against stock `pulumi preview` on `aws-ts`, `aws-python`, `aws-go`, `aws-yaml` and `aws-csharp`: the same steps, differing only in the names of autonamed resources ([`baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt)).
- `plunge up` of `aws-ts` on `plunge+file://`, then `stack export` and `stack output`: every taggable resource marked and the bucket policy unmarked, with deterministic names on the queues and the topic (chant [`plunge-outputs/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-outputs)).
- `plunge drift --resource --stream`: one drifted bucket, one role in sync, one component not observed, one provider Read each (chant [`plunge-drift/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-drift)).
- `plunge stack adopt --json`: would-adopt, adopted, already-adopted, refused (behold [fixture](https://github.com/INTENTIUS/behold/tree/main/src/__fixtures__/plunge-adopt-30806be26b)).

Each shows behaviour on an emulator, and none on AWS.

## Waiting for a run

| Run | Issue |
|---|---|
| The S3 record store on floci, and two updates on different resources of one stack | [#6](https://github.com/INTENTIUS/plunge/issues/6) |
| Adopting a corpus stack end to end, the first update, the back-out, and races C1 to C6 | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| C1 races on pulumi-aws and aws-native | [#10](https://github.com/INTENTIUS/plunge/issues/10), [#36](https://github.com/INTENTIUS/plunge/issues/36) |
| An IAM gate on `plunge-stack` | [#45](https://github.com/INTENTIUS/plunge/issues/45) |

## Tested, not recorded

On 2026-10-27 the Go suites passed, and a clean `plunge up` and destroy of
each `aws-*` corpus program on floci showed no sweep warning. Each destroy was
refused on the SSM parameters, whose tags floci drops
([lex00/floci#263](https://github.com/lex00/floci/issues/263)), as the live
marker check requires. That closed
[#28](https://github.com/INTENTIUS/plunge/issues/28),
[#31](https://github.com/INTENTIUS/plunge/issues/31) and
[#37](https://github.com/INTENTIUS/plunge/issues/37); none of these runs is
recorded as a claim.

## Run one now

The [Tutorial](/plunge/use/tutorial/) repeats the `aws-ts` run on floci.
