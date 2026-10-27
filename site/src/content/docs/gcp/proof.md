---
title: Proof
description: Recorded runs on Google Cloud and the runs still to come.
prompt: |
  Follow https://intentius.io/plunge/gcp/proof/ and report which GCP claims have a recorded run. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Recorded runs

- `plunge preview` against stock `pulumi preview` on `gcp-yaml`, on floci-gcp, 2026-10-26: `MATCH` ([`baseline-verdicts.txt`](https://github.com/INTENTIUS/plunge/blob/main/tests/plunge-corpus/baseline-verdicts.txt), [Claims and runs](/plunge/claims/)).

That is a preview; nothing has been applied on floci-gcp or Google Cloud.

## Waiting for a run

| Run | Issue |
|---|---|
| The `plunge+gs://` store on floci-gcp | [#14](https://github.com/INTENTIUS/plunge/issues/14), [#34](https://github.com/INTENTIUS/plunge/issues/34) |
| Adoption and the races | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| floci-gcp list paths | [#32](https://github.com/INTENTIUS/plunge/issues/32) |
| No token fetched from Google with local endpoints | [#42](https://github.com/INTENTIUS/plunge/issues/42) |
| A gate | [#45](https://github.com/INTENTIUS/plunge/issues/45) |
