---
title: Proof
description: The recorded run on floci-az, the Azure emulator, and the runs still to come. Nothing has run on Azure itself.
prompt: |
  Follow https://intentius.io/plunge/azure/proof/ and report which Azure claims have a recorded run. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Recorded runs

- `plunge preview` against stock `pulumi preview` on `azure-yaml`, on floci-az, 2026-10-26: `DIFF-INPUTS`, where only the autonamed resource group `scratch-rg` differs ([`baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt), [Claims and runs](/plunge/claims/)).

That is the only recorded run. On 2026-10-27 a clean `plunge up` and destroy
of `azure-yaml` on floci-az showed no sweep warning and no real-cloud call
([#41](https://github.com/INTENTIUS/plunge/issues/41), closed); it is tested,
not recorded. Nothing has run on Azure.

## Waiting for a run

| Run | Issue |
|---|---|
| The `plunge+azblob://` store on floci-az | [#14](https://github.com/INTENTIUS/plunge/issues/14) |
| ARM list consistency on a real subscription | [#15](https://github.com/INTENTIUS/plunge/issues/15) |
| Adoption and the races | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| A gate | [#45](https://github.com/INTENTIUS/plunge/issues/45) |
