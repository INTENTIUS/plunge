---
title: Gate
description: An admission policy on the plunge-stack label can fence writes by stack. Not yet written for plunge.
prompt: |
  Follow https://intentius.io/plunge/kubernetes/gate/. Compute the plunge-stack digest for this stack and draft the admission policy match for it. Show it to me; apply nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
The label's value is a digest, so a policy names the digest, not the stack:

```sh
printf %s 'myproject/prod' | shasum -a 256 | cut -c1-16
```

That is how `pkg/backend/plunge/markers/keys.go` computes it: the first 16
hex characters of the SHA-256 of `<project>/<stack>`.

## The record store's own fence

`plunge+k8s://` labels each record Secret with an estate (the `estate` query
key, else the namespace). choudoufu's estate-boundary
ValidatingAdmissionPolicy, embedded in choudoufu's `staterecord` package
(`pkg/staterecord/estate-boundary.yaml` in choudoufu), fences writes to those
Secrets by that label.

## Objects plunge creates

choudoufu's policy reads its own `tofu-estate` label. A policy reading
`plunge-stack` on the objects plunge creates is not written, and nothing has
run. [#45](https://github.com/INTENTIUS/plunge/issues/45) holds it.
