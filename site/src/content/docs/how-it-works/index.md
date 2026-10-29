---
title: Overview
description: How plunge keeps ownership on the resources and state in one record per resource.
prompt: |
  Read https://intentius.io/plunge/how-it-works/ and explain the changes plunge would make for the Pulumi program in this repository. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

plunge, a fork of [pulumi/pulumi](https://github.com/pulumi/pulumi), runs the
same programs, SDKs and providers, and changes where state lives and how a run
decides ownership.

## Three words

A stack is the same as in Pulumi: one program's deployment, named
`<project>/<stack>`.

A [marker](../glossary/#marker) is a tag or label on the live object naming
its stack and its URN: `plunge-stack` and `plunge-urn` ([Identity](identity/)).

A [record](../glossary/#record) is one resource's state in a store with conditional writes, under
`stacks/<project>/<stack>/resources/<urn>` ([Records](records/)).

## The marker names the owning stack

plunge adds both markers to each taggable resource's inputs after every
transform, so the create call writes them and any tool can list a stack's
objects. On AWS:

```
aws resourcegroupstaggingapi get-resources \
  --tag-filters Key=plunge-stack,Values=myproject/dev
```

Before creating a type a lister covers, plunge lists live objects with the
resource's markers. If it finds one, it [binds](../glossary/#bind) it instead
of creating a second.

## On a plunge backend

Stock Pulumi keeps one checkpoint file per stack and orders updates with its
stack lock. With `plunge+<store>`, each row below works differently.

| | On `plunge+<store>` |
|---|---|
| Where state lives | one record per resource, per root output, and one for the secrets configuration |
| Two updates at once | both run; [a conditional write per record](concurrency/) decides any clash |
| A run that would delete an object | [checks the live marker first](deletes/) |
| An unnamed resource | gets the [same name on every run](names/) |
| A saved plan applied after the state moved | refused by name, exit 40, when any record it read has moved |
| Stack history and backups | not kept; the commands are refused by name |

The DIY backend at the URL inside `plunge+` keeps everything but state (the
stack list, stack tags, the default secrets manager), as for stock Pulumi.

Source: `pkg/backend/plunge/backend.go`.

## Lineage

In [chant](https://intentius.io/chant/), identity markers on the live object
decide ownership. plunge carries that into Pulumi, as
[choudoufu](https://intentius.io/choudoufu/) does into OpenTofu. Both are chant
engines (a chant workspace can run a plunge stack as a member), and plunge
runs any Pulumi program without chant.

## Upstream

plunge forks [pulumi/pulumi](https://github.com/pulumi/pulumi) at 7e12743da8
(after the 3.272.0 release) under Apache-2.0, and merges upstream without
rebasing. The scheduled merge of 2026-10-26 stopped on a conflict in
`sdk/go/auto/automation/commands.go` and waits for a person
([#24](https://github.com/INTENTIUS/plunge/issues/24)). Until a person resolves
it, the fork stays on the upstream release it started from. The project has no tie to Pulumi Corporation, owner of the Pulumi
trademark.
