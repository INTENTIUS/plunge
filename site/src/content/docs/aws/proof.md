---
title: Proof
description: Recorded runs on floci, the AWS emulator, with dates and recordings.
prompt: |
  Follow https://intentius.io/plunge/aws/proof/. Open each recording it links and check the page's description of it. Report any difference. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Recorded runs

These runs were recorded on 2026-10-26 on floci, the AWS emulator
([Claims and runs](/plunge/claims/)):

- `plunge preview` against stock `pulumi preview` on `aws-ts`, `aws-python`, `aws-go`, `aws-yaml` and `aws-csharp`: the same steps, differing only in the names of autonamed resources ([`baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt)).
- `plunge up` of `aws-ts` on `plunge+file://`, then `stack export` and `stack output`: every taggable resource marked and the bucket policy unmarked, with deterministic names on the queues and the topic (chant [`plunge-outputs/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-outputs)).
- `plunge drift --resource --stream`: one drifted bucket, one role in sync, one component not observed, one provider Read each (chant [`plunge-drift/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-drift)).
- `plunge stack adopt --json`: would-adopt, adopted, already-adopted, refused (behold [fixture](https://github.com/INTENTIUS/behold/tree/main/src/__fixtures__/plunge-adopt-30806be26b)).

## Run one now

The [Tutorial](/plunge/use/tutorial/) repeats the `aws-ts` run on floci.
