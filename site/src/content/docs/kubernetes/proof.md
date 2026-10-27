---
title: Proof
description: What has run on Kubernetes, and what waits for a run.
prompt: |
  Follow https://intentius.io/plunge/kubernetes/proof/ and report which Kubernetes claims have a recorded run. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Recorded runs

None. No corpus program uses pulumi-kubernetes, and none of the four recorded
runs touches a cluster ([Claims and runs](/plunge/claims/)).

## Waiting for a run

| What | Issue |
|---|---|
| The `plunge+k8s://` store: a create, an unchanged re-run and a destroy | [#14](https://github.com/INTENTIUS/plunge/issues/14) |
| Adoption and the races | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| A gate on the `plunge-stack` label | [#45](https://github.com/INTENTIUS/plunge/issues/45) |
