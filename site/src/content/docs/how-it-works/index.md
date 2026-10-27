---
title: What it is
description: How plunge keeps ownership on the resources and state in one record per resource.
prompt: |
  Read https://intentius.io/plunge/how-it-works/ and explain what plunge would change for the Pulumi program in this repository. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

plunge is a fork of [pulumi/pulumi](https://github.com/pulumi/pulumi) that runs
the same programs, SDKs and providers. What changes is where a stack's state
lives and how a run decides what it owns.

The ownership model comes from [chant](https://intentius.io/chant/): identity
markers on the live object decide what a deployment owns. This fork carries
that model into Pulumi, as [choudoufu](https://intentius.io/choudoufu/) carries
it into OpenTofu. Both are engines for chant (a chant workspace can run a
plunge stack as a member) and each works on its own, running any Pulumi program
with no chant project.

## Three words

A stack is what it is in Pulumi: one program's deployment, named
`<project>/<stack>`.

A marker is a tag or label on the live object naming its stack and its URN:
`plunge-stack` and `plunge-urn` ([Identity](identity/)).

A record is one resource's state in a store with conditional writes, under
`stacks/<project>/<stack>/resources/<urn>` ([Records](records/)).

## The marker says what the stack owns

plunge adds both markers to each taggable resource's inputs, after every
transform has run, so the provider writes them in the create call. Any tool
can then list a stack's objects. On AWS:

```
aws resourcegroupstaggingapi get-resources \
  --tag-filters Key=plunge-stack,Values=myproject/dev
```

Before each create, where a lister covers the type, plunge lists its live objects carrying the
resource's markers, and binds one it finds instead of creating a second.

## A DIY backend and a plunge backend

| | Stock DIY backend | `plunge+<store>` |
|---|---|---|
| Where state lives | one checkpoint file per stack | one record per resource, per root output, and one for the secrets configuration |
| Two updates at once | the second waits for the stack lock or fails | both run; [a conditional write per record](concurrency/) decides any clash |
| A run that would delete an object | deletes what the checkpoint names | [checks the live marker first](deletes/) |
| An unnamed resource | gets a random suffix | gets the [same name on every run](names/) |
| A saved plan applied after the state moved | applied when the program still matches the plan | refused by name, exit 40, when any record it read has moved |
| Stack history and backups | kept | not kept; the commands are refused by name |

The DIY backend at the URL inside `plunge+` keeps everything that is not
state (which stacks exist, their tags, the default secrets manager), as it
does for stock Pulumi (`pkg/backend/plunge/backend.go`).

## What has run

The behaviour above is in the code and unit-tested. Four runs are recorded so
far, all on local emulators: [Claims and runs](../claims/) lists them and the
issue that holds each other run.

## Upstream

plunge forks [pulumi/pulumi](https://github.com/pulumi/pulumi) at 7e12743da8
(after the 3.272.0 release) under Apache-2.0, merges upstream in and never
rebases. It is not affiliated with Pulumi Corporation, owner of the Pulumi
trademark.
