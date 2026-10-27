---
title: Start a new stack
description: A five-minute offline quickstart, then installing plunge for real, choosing a plunge+ backend URL and running the first update.
prompt: |
  Follow https://intentius.io/plunge/use/start/. Install plunge from its v0.1.0 release, choose a plunge+ URL for this project with me, and run `plunge preview`. Report the preview.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

## Quickstart

This takes about five minutes. It needs neither a cloud account nor Docker,
and downloads no provider. It installs plunge, keeps state in a local directory, and
previews a one-line YAML program. These commands ran as written on macOS on
arm64; set `OS` and `ARCH` for your machine.

1. Download the release, check it and unpack it:

   ```sh
   V=v0.1.0 OS=linux ARCH=x64    # or OS=darwin, ARCH=arm64
   U=https://github.com/INTENTIUS/plunge/releases/download/plunge-$V
   curl -fLO "$U/plunge-$V-$OS-$ARCH.tar.gz"
   curl -fLO "$U/plunge-$V-checksums.txt"
   sha256sum -c --ignore-missing plunge-$V-checksums.txt   # macOS: shasum -a 256 -c --ignore-missing
   tar -xzf plunge-$V-$OS-$ARCH.tar.gz                    # unpacks plunge/
   export PATH="$PWD/plunge:$PATH"
   plunge version
   ```

   `plunge version` prints `plunge v0.1.0` and `pulumi v3.272.0`.

2. Log in to a local [plunge+ store](../../glossary/#plunge-store):

   ```sh
   export PULUMI_SKIP_UPDATE_CHECK=true PULUMI_CONFIG_PASSPHRASE=quickstart
   mkdir -p "$HOME/plunge-quickstart/state" "$HOME/plunge-quickstart/hello"
   plunge login "plunge+file://$HOME/plunge-quickstart/state"
   ```

3. Write a tiny program:

   ```sh
   cd "$HOME/plunge-quickstart/hello"
   cat > Pulumi.yaml <<'YAML'
   name: hello
   runtime: yaml
   outputs:
     greeting: hello from plunge
   YAML
   ```

4. Create a stack and preview it:

   ```sh
   plunge stack init dev
   plunge preview
   ```

   The preview plans one create, the stack itself, with the output
   `greeting: "hello from plunge"`.

5. Optionally, run the update and look at the records:

   ```sh
   plunge up --yes
   plunge stack output greeting
   find "$HOME/plunge-quickstart/state/.pulumi/records" -type f
   ```

   The store now holds three records: the stack resource, the `greeting`
   output and the secrets configuration ([Records](../../how-it-works/records/)).

To clean up, run `plunge destroy --yes`, `plunge stack rm dev --yes` and
`plunge logout`, then delete `$HOME/plunge-quickstart`.

`plunge new yaml` also works, though it fetches its template from GitHub, so
it needs the network. Run it after `plunge login`; before a login it stops
with `could not determine organization name`.

## Install

The [releases page](https://github.com/INTENTIUS/plunge/releases/tag/plunge-v0.1.0)
has plunge `v0.1.0` (Pulumi-compatible `3.272.0`). There is a `.tar.gz` for
darwin and linux and a `.zip` for windows, each on arm64 and x64. Step 1 of
the quickstart downloads, checks and unpacks one.

Keep the `plunge/` directory whole. The CLI looks beside its binary for the
seven bundled `pulumi-language-*` hosts: five built from plunge's tree, and
pulumi's dotnet and yaml builds. It installs Java and HCL on first use, and
providers download as they do for stock Pulumi. Through a symlink named
`pulumi`, `plunge version` prints only the Pulumi version, which the
Automation API reads.

The `export PATH` line lasts for one shell. To keep it, move `plunge/`
somewhere stable and add the line to your shell profile:

```sh
mkdir -p "$HOME/.local/opt" && mv plunge "$HOME/.local/opt/plunge"
echo 'export PATH="$HOME/.local/opt/plunge:$PATH"' >> ~/.zshrc   # or ~/.bashrc
```

The macOS binaries are ad hoc signed and not notarized. `curl` sets no
quarantine flag, but a browser download does; clear it before the first run:

```sh
xattr -dr com.apple.quarantine plunge
```

On Windows the zip unpacks to `plunge\bin\`, with `plunge.exe` and the
hosts; add that directory to `PATH`. The Windows binaries are unsigned, so
SmartScreen may warn. Check the zip with
`Get-FileHash plunge-v0.1.0-windows-x64.zip` against the checksums file.

### Uninstall

plunge shares `~/.pulumi` with stock Pulumi, and `plunge login` changes the
backend stock `pulumi` uses there too. Run `plunge logout` first, then delete
the `plunge/` directory and the `PATH` line in your shell profile. Plugins
plunge downloaded stay in `~/.pulumi/plugins`, where stock Pulumi uses them.

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
