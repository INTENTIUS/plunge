---
title: Operate
description: Rename, remove, apply a saved plan, and run twice at once on Kubernetes.
prompt: |
  Follow https://intentius.io/plunge/kubernetes/operate/ for my change. Edit the program, run `plunge preview --save-plan plan.json` and report the plan.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
## Rename

A Pulumi rename is an `aliases` entry. What plunge does with the
`plunge-urn` label and the record key is open on [#46](https://github.com/INTENTIUS/plunge/issues/46).

## Remove

Delete the object from the program and update. plunge checks the live
object's labels before it deletes ([#37](https://github.com/INTENTIUS/plunge/issues/37), built, waiting for its run).

## Plan, review, update

`plunge preview --save-plan plan.json`, then `plunge up --plan plan.json`.
A moved record refuses the update by name, exit 40.

## Two runs at once

There is no stack lock. Before a create, plunge LISTs the kind by label
selector, which the API server answers from a quorum read, so a create that
landed is seen. One object found is bound; two are refused by name.
Kubernetes names are not autonamed by plunge: `metadata.name` keeps upstream's
behaviour, so C1 does not cover them. Unit-tested on other families; nothing
has run on a cluster.

## Records in the cluster

`plunge+k8s://namespace/prefix?metadata=<diy url>` keeps one Secret per
record, written conditionally on its `resourceVersion`
([Records](/plunge/how-it-works/records/)).
