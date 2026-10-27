---
title: Proof
description: Recorded runs on Kubernetes and the runs still to come.
prompt: |
  Follow https://intentius.io/plunge/kubernetes/proof/ and report which Kubernetes claims have a recorded run. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Recorded runs

No corpus program uses pulumi-kubernetes, and no recorded run touches a
cluster ([Claims and runs](/plunge/claims/)).

## Waiting for a run

| Run | Issue |
|---|---|
| The `plunge+k8s://` store: a create, an unchanged re-run and a destroy | [#14](https://github.com/INTENTIUS/plunge/issues/14) |
| Adoption and the races | [#7](https://github.com/INTENTIUS/plunge/issues/7) |
| A gate on the `plunge-stack` label | [#45](https://github.com/INTENTIUS/plunge/issues/45) |
