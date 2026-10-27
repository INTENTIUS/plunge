---
title: Operate
description: Rename, remove, apply a saved plan, and run twice at once on Kubernetes.
prompt: |
  Follow https://intentius.io/plunge/kubernetes/operate/ for my change. Edit the program, run `plunge preview --save-plan plan.json` and report the plan.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Rename

A Pulumi rename is an `aliases` entry. Its effect on the `plunge-urn` label
and the record key is open on [#46](https://github.com/INTENTIUS/plunge/issues/46).

## Remove

Delete the object from the program and update; plunge checks the live
object's labels first ([#37](https://github.com/INTENTIUS/plunge/issues/37), tested).

## Plan, review, update

Run `plunge preview --save-plan plan.json`, then `plunge up --plan plan.json`,
which is refused by name, exit 40, if a record has moved.

## Two runs at once

There is no stack lock. Before a create, plunge LISTs the kind by label
selector, answered from a quorum read, so a landed create is seen. One object
found is bound; two are refused by name. plunge leaves `metadata.name` to
upstream's behaviour, so C1 does not cover it.

## Records in the cluster

`plunge+k8s://namespace/prefix?metadata=<diy url>` keeps one Secret per
record, written conditionally on its `resourceVersion`
([Records](/plunge/how-it-works/records/)).
