---
title: Drift of one resource
description: plunge drift reads one resource's record and calls its provider's Read once, with no program run and no stack-wide refresh.
prompt: |
  Read https://intentius.io/plunge/how-it-works/drift/. Run `plunge drift --resource <urn> --stream` for the URN I name and report the drift-record line. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

```
plunge drift --resource 'urn:pulumi:dev::app::aws:s3/bucket:Bucket::logs' --stream
```

It reads the resource's record and its provider's record by key, without
assembling the stack's snapshot, configures the provider from its record,
calls Read once, and compares the live outputs with the recorded ones
(`pkg/cmd/pulumi/drift/drift.go`). The program does not run, so its inputs
are not compared. It needs a `plunge+` backend.

## The stream

With `--stream` the output is chant's
[drift record v1](https://github.com/INTENTIUS/chant/blob/main/docs/src/content/docs/reference/drift-record.mdx)
NDJSON on stdout: a `drift-start` line, the resource's `drift-record` line,
and a `drift-settle` line whose `reads` counts the record reads, the provider
Reads and the stack refreshes. `--member` sets chant's member name, which
defaults to the stack name.

## The recorded run

On 2026-10-26, against the `aws-ts` corpus stack on floci, after a tag was
changed through floci's API (chant
[`plunge-drift/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-drift)):

| Resource | Status | Reads |
|---|---|---|
| the bucket `docs-assets` | `drifted`, with `tags["corpus-owner"]` before `plunge` and after `hotfix` | 3 records, 1 provider Read, 0 refreshes |
| the role `app-role` | `in-sync` | 3 records, 1 provider Read, 0 refreshes |
| the component `docs` | `not-observed`, `unsupported-kind`: a component has no provider Read | 1 record, 0 provider Reads, 0 refreshes |

A URN the stack holds no record for exits 1 with `the stack holds no record
for <urn>` and prints no stream.

## Against choudoufu

choudoufu's `live-plan -resource=ADDR` plans one resource and its upstream
cone. Both emit drift record v1, so chant reads either the same way.
