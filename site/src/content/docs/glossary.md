---
title: Glossary
description: The words this site uses, each in a sentence or two, with a link to the page that covers it.
prompt: |
  Read https://intentius.io/plunge/glossary/ and explain the terms I ask about, in the context of this repository. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

## Record

A record is one resource's state, kept as its own object in a store. Stock
Pulumi keeps a whole stack's state in one checkpoint file. Here there is one
per resource, so two runs can write to different resources at the same time
([Records](../how-it-works/records/)).

## Marker

A marker is a tag or label written on each cloud object a stack manages.
`plunge-stack` names the owning stack and `plunge-urn` names the resource.
Both are read before an object is bound or deleted
([Identity](../how-it-works/identity/)).

## plunge+ store

A plunge+ store holds a stack's state, one object per resource. A backend URL
with a `plunge+` prefix names it, such as `plunge+file://` or `plunge+s3://`
(the five are listed on Records). Each store writes an object only if nobody
changed it since it was read
([Records](../how-it-works/records/#the-five-stores)).

## C1 and C2

These are the two concurrency claims about creates. In the first, two runs
create the same resource; its fixed name makes the cloud refuse the second
create, and the loser's next run binds the winner's object
([Names](../how-it-works/names/#c1-two-creates-of-one-resource)). In the
second, live objects carrying a resource's markers are listed before the
resource is created. A run binds the one it finds and refuses the run when it
finds two
([Resource-level concurrency](../how-it-works/concurrency/#c2-the-listing-behind-the-collision-check)).

## C1 to C6

The race script `scripts/plunge-races/races.sh` runs six races on floci.
The first two are the creates above. The third applies a saved plan after
its state moved, which must be refused. The fourth runs an update
against a destroy of the same resource. In the fifth, two updates of one
stack run at once, first on different resources, then on the same one. The
sixth kills a run part way and checks that a re-run finishes it. None of the
six has run yet ([#7](https://github.com/INTENTIUS/plunge/issues/7)).

## floci

[floci](https://github.com/lex00/floci) is a free AWS emulator that runs
locally in Docker. floci-gcp and floci-az emulate Google Cloud and Azure.
Every run on the claims page used one of them, and none touched a real
cloud ([Claims and runs](../claims/)).

## chant

[chant](https://intentius.io/chant/) is an infrastructure tool, also from
Intentius. It decides ownership from markers on live objects, and this fork brings
that idea to Pulumi. A chant workspace can run such a stack as one of its members.

## choudoufu

[choudoufu](https://intentius.io/choudoufu/) applies chant's ownership markers
to OpenTofu. Its record store package, and the conformance tests for it,
are the ones this fork imports.

## Estate

An estate is a named set of cloud objects one team manages, in choudoufu's
terms. On this site the word appears only as a label on the Secrets a
`plunge+k8s://` store writes, which choudoufu's admission policy reads
([Records](../how-it-works/records/#the-five-stores)).

## Sweep

The sweep runs after `plunge up` and `plunge preview`. It lists each type the
run marked and warns about any live object that carries this stack's marker
while the stack's state does not name it. It never deletes
([Resource-level concurrency](../how-it-works/concurrency/)).

## Bind

To bind is to take an existing live object as a resource's object instead of
creating a second one. A run binds an object whose markers name the resource
([Identity](../how-it-works/identity/)).

## Lexicon

A lexicon is a chant package for one platform or engine. chant's Pulumi
lexicon reads the stack output and drift stream, and its test data holds two
of the recorded runs.

## Adopt

To adopt is to move a stock Pulumi stack onto a plunge+ store with
`plunge stack adopt`. Each resource keeps its ID, and the next `plunge up`
adds the markers ([Migrate a stock stack](../use/migrate/)).
