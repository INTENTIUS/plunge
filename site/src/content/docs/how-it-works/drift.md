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

It reads the resource's and provider's records by key, without the snapshot,
and configures the provider from its record. One Read gives the outputs to
compare. The program does not run, so inputs are not compared. The command
needs a `plunge+` backend.

## The stream

With `--stream` the output is chant's
[drift record v1](https://github.com/INTENTIUS/chant/blob/main/docs/src/content/docs/reference/drift-record.mdx)
NDJSON on stdout, a `drift-start` line and the resource's `drift-record`
line followed by `drift-settle`. Its `reads` counts record reads, provider
Reads and refreshes. `--member`
sets chant's member name (default: the stack name).

## The recorded run

On 2026-10-26, against the `aws-ts` corpus stack on floci, after a tag was
changed through floci's API (chant
[`plunge-drift/`](https://github.com/INTENTIUS/chant/tree/main/lexicons/pulumi/src/testdata/plunge-drift)):

| Resource | Status | Reads |
|---|---|---|
| the bucket `docs-assets` | `drifted`, with `tags["corpus-owner"]` before `plunge` and after `hotfix` | 3 records, 1 provider Read, 0 refreshes |
| the role `app-role` | `in-sync` | 3 records, 1 provider Read, 0 refreshes |
| the component `docs` | `not-observed`, `unsupported-kind`: a component has no provider Read | 1 record, 0 provider Reads, 0 refreshes |

An unrecorded URN exits 1 with `the stack holds no record for <urn>` and no
stream.

## Against choudoufu

choudoufu's `live-plan -resource=ADDR` plans one resource and its upstream
cone. chant reads its drift record v1 the same way.

Source: `pkg/cmd/pulumi/drift/drift.go`.
