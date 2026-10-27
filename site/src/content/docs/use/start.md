---
title: Start a new stack
description: Build plunge, choose a plunge+ backend URL, and run the first update.
prompt: |
  Follow https://intentius.io/plunge/use/start/. Build plunge, choose a plunge+ URL for this project with me, and run `plunge preview`. Report the preview.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

## Install

There is no plunge release yet ([#13](https://github.com/INTENTIUS/plunge/issues/13)).
Build it from a checkout:

```sh
make plunge                  # bin/plunge, both versions stamped (needs mise)
# or, without mise:
go build -C pkg -o ../bin/plunge -ldflags "$(./scripts/plunge-version.sh ldflags)" ./cmd/pulumi
make plunge_language_hosts   # the bundled dotnet and yaml hosts
```

`plunge version` prints plunge's own version and the Pulumi version it is
compatible with. Invoked through a symlink named `pulumi`, it prints only the
latter, which is what the Automation API reads.

## Choose a backend

| You have | URL |
|---|---|
| one machine, for trying it | `plunge+file:///path/to/dir` |
| an S3 bucket | `plunge+s3://bucket/prefix?region=us-east-1` |
| a GCS bucket | `plunge+gs://bucket/prefix` |
| an Azure storage account | `plunge+azblob://container/prefix?storage_account=acct&resource_group=rg&subscription_id=<sub>` |
| a Kubernetes namespace | `plunge+k8s://namespace/prefix?metadata=s3://bucket/meta` |

A bucket must have versioning, a noncurrent-version lifecycle rule and its
public access blocked before the store opens
([What you set up by hand](../setup/)). The local store holds a lock file for
one write at a time on one host only.

```sh
plunge login 'plunge+s3://my-records/prod?region=us-east-1'
plunge stack init dev
plunge preview
plunge up
```

Or name it in `Pulumi.yaml`, which is also how chant picks plunge:

```yaml
backend:
  url: plunge+s3://my-records/prod?region=us-east-1
```

## Check the markers

After the first update, list the stack's objects by its marker. On AWS:

```sh
aws resourcegroupstaggingapi get-resources \
  --tag-filters Key=plunge-stack,Values=<project>/dev
```

On GCP and Kubernetes the label holds a digest, not the name
([Identity](../../how-it-works/identity/)).

## What has run

The local store has recorded runs on floci. The S3, GCS, Azure Blob and
Kubernetes stores wait for theirs
([#6](https://github.com/INTENTIUS/plunge/issues/6),
[#14](https://github.com/INTENTIUS/plunge/issues/14)).
