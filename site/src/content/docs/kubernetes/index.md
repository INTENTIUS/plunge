---
title: Kubernetes
description: Two labels mark each object, as digests. Records can live in the cluster.
prompt: |
  Read https://intentius.io/plunge/kubernetes/ and tell me which of its pages applies to my task. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
Each pulumi-kubernetes object carries `plunge-stack` and `plunge-urn` labels. Label values are short, so both are hex digests (16 characters and 32).

- [Adopt](/plunge/kubernetes/adopt/): bring a stock stack's resources onto records.
- [Gate](/plunge/kubernetes/gate/): fence writes by the owning stack.
- [Operate](/plunge/kubernetes/operate/): rename, remove, saved plans, two runs at once.
- [Compatibility](/plunge/kubernetes/compatibility/): which providers and types take markers, and how they are listed.
- [Proof](/plunge/kubernetes/proof/): recorded runs on Kubernetes and the runs still to come.
