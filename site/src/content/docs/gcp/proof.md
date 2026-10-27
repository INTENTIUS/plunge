---
title: Proof
description: The recorded run on floci-gcp, the Google Cloud emulator.
prompt: |
  Follow https://intentius.io/plunge/gcp/proof/ and report which GCP claims have a recorded run. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Recorded runs

- `plunge preview` against stock `pulumi preview` on `gcp-yaml`, on floci-gcp, 2026-10-26: `MATCH` ([`baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt), [Claims and runs](/plunge/claims/)).
