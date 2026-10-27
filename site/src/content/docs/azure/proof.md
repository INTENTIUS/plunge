---
title: Proof
description: The recorded run on floci-az, the Azure emulator.
prompt: |
  Follow https://intentius.io/plunge/azure/proof/ and report which Azure claims have a recorded run. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Recorded runs

- `plunge preview` against stock `pulumi preview` on `azure-yaml`, on floci-az, 2026-10-26: `DIFF-INPUTS`, where only the autonamed resource group `scratch-rg` differs ([`baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt), [Claims and runs](/plunge/claims/)).
