---
title: Proof
description: What has run on Azure, and what waits for a run.
prompt: |
  Follow https://intentius.io/plunge/azure/proof/ and report which Azure claims have a recorded run. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Recorded runs

- `plunge preview` against stock `pulumi preview` on `azure-yaml`, on floci-az, 2026-10-26: `DIFF-INPUTS`, where only the autonamed resource group `scratch-rg` differs ([`baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt), [Claims and runs](/plunge/claims/)).

That is a preview. Nothing has been applied on floci-az or Azure.

## Waiting for a run

| What | Issue |
|---|---|
| The `plunge+azblob://` store on floci-az | [#14](https://github.com/INTENTIUS/plunge/issues/14) |
| ARM list consistency on a real subscription | [#15](https://github.com/INTENTIUS/plunge/issues/15) |
| ARM listings through `metadataHost` | [#41](https://github.com/INTENTIUS/plunge/issues/41) |
| Adoption and the races | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| A gate | [#45](https://github.com/INTENTIUS/plunge/issues/45) |
