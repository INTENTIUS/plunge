---
title: Start a new stack
description: Install plunge from its release, choose a plunge+ backend URL, and run the first update.
prompt: |
  Follow https://intentius.io/plunge/use/start/. Install plunge from its v0.1.0 release, choose a plunge+ URL for this project with me, and run `plunge preview`. Report the preview.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

## Install

plunge `v0.1.0` (Pulumi-compatible `3.272.0`) is on the
[releases page](https://github.com/INTENTIUS/plunge/releases/tag/plunge-v0.1.0).
There is a `.tar.gz` for darwin and linux and a `.zip` for windows, each on
arm64 and x64.

```sh
V=v0.1.0 OS=linux ARCH=x64    # or darwin, arm64
U=https://github.com/INTENTIUS/plunge/releases/download/plunge-$V
curl -fLO "$U/plunge-$V-$OS-$ARCH.tar.gz"
curl -fLO "$U/plunge-$V-checksums.txt"
sha256sum -c --ignore-missing plunge-$V-checksums.txt   # macOS: shasum -a 256 -c --ignore-missing
tar -xzf plunge-$V-$OS-$ARCH.tar.gz                    # unpacks plunge/
export PATH="$PWD/plunge:$PATH"
plunge version
```

`plunge version` prints `plunge v0.1.0` and `pulumi v3.272.0`. Through a
symlink named `pulumi` it prints only the Pulumi version, which the
Automation API reads.

Keep the `plunge/` directory whole. The CLI looks beside its binary for the
seven bundled `pulumi-language-*` hosts: five built from plunge's tree and
pulumi's dotnet and yaml builds. It installs Java and HCL on first use, and
providers download as for stock Pulumi.

The macOS binaries are ad hoc signed and not notarized. `curl` sets no
quarantine flag; after a browser download, clear it before the first run:

```sh
xattr -dr com.apple.quarantine plunge
```

On Windows the zip unpacks to `plunge\bin\`, with `plunge.exe` and the
hosts; add that directory to `PATH`. The Windows binaries are unsigned, so
SmartScreen may warn. Check the zip with
`Get-FileHash plunge-v0.1.0-windows-x64.zip` against the checksums file.

### Build from source

For contributors, from a checkout of
[INTENTIUS/plunge](https://github.com/INTENTIUS/plunge):

```sh
make plunge                  # bin/plunge, both versions stamped (needs mise)
# or, without mise:
go build -C pkg -o ../bin/plunge -ldflags "$(./scripts/plunge-version.sh ldflags)" ./cmd/pulumi
make plunge_language_hosts   # the bundled dotnet and yaml hosts
```

A source build reports `plunge v0.1.0-dev.0`. The
[README](https://github.com/INTENTIUS/plunge/blob/main/README.md#building)
covers the rest.

## Choose a backend

| You have | URL |
|---|---|
| one machine, for trying it | `plunge+file:///path/to/dir` |
| an S3 bucket | `plunge+s3://bucket/prefix?region=us-east-1` |
| a GCS bucket | `plunge+gs://bucket/prefix` |
| an Azure storage account | `plunge+azblob://container/prefix?storage_account=acct&resource_group=rg&subscription_id=<sub>` |
| a Kubernetes namespace | `plunge+k8s://namespace/prefix?metadata=s3://bucket/meta` |

The store opens a bucket only with versioning on, a noncurrent-version
lifecycle rule and public access blocked ([Set up by hand](../setup/)). The
local store's lock file allows one write at a time, on one host.

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

After the first update, list the stack's objects by marker, for example on AWS:

```sh
aws resourcegroupstaggingapi get-resources \
  --tag-filters Key=plunge-stack,Values=<project>/dev
```

On GCP and Kubernetes the label holds a digest in place of the name
([Identity](../../how-it-works/identity/)).

## Recorded runs

The local store has recorded runs on floci. The S3, GCS, Azure Blob and
Kubernetes stores wait for theirs
([#6](https://github.com/INTENTIUS/plunge/issues/6),
[#14](https://github.com/INTENTIUS/plunge/issues/14)). choudoufu's store
conformance suite passed against scratch emulators on 2026-10-27 for
`plunge+file://`, `plunge+gs://` and `plunge+azblob://`; `plunge+s3://` passed
16 of 18, since floci ignores `If-Match` on `DeleteObject`
([lex00/floci#263](https://github.com/lex00/floci/issues/263)).
