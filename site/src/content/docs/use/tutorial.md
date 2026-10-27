---
title: Tutorial
description: Run the aws-ts corpus program under plunge on the floci emulator, see the markers and the records, and read one resource's drift.
prompt: |
  Follow https://intentius.io/plunge/use/tutorial/ up to step 5. Build plunge, start floci, log in to the plunge+file:// backend and run `plunge preview --stack floci`, then report the preview. I run `plunge up` myself.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

The recorded `plunge-outputs` and `plunge-drift` files came from a run like
this, with its own name prefix ([Claims and runs](../../claims/)). It all runs
locally against the floci AWS emulator, and nothing reaches AWS.

You need Docker, Go, Node.js and a plunge checkout to build from, since there is
no release yet ([#13](https://github.com/INTENTIUS/plunge/issues/13)).

## 1. Build plunge

```sh
cd plunge
go build -C pkg -o ../bin/plunge -ldflags "$(./scripts/plunge-version.sh ldflags)" ./cmd/pulumi
make plunge_language_hosts
export PATH="$PWD/bin:$PATH"
plunge version
```

## 2. Start floci

The image is the one choudoufu pins:

```sh
CHDF=/path/to/choudoufu
docker run -d --rm --name plunge-floci -p 127.0.0.1:4566:4566 "$(cat $CHDF/live/floci-image)"
```

## 3. Keep the shell away from AWS

```sh
unset AWS_PROFILE AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
export AWS_ENDPOINT_URL=http://localhost:4566
export PULUMI_CONFIG_PASSPHRASE=plunge-baseline
export PULUMI_SKIP_UPDATE_CHECK=true
```

The stack file `tests/plunge-corpus/aws-ts/Pulumi.floci.yaml` points every
provider call at floci with fake keys.

## 4. Log in to a plunge backend

```sh
export PULUMI_BACKEND_URL="plunge+file://$(mktemp -d)"
cd tests/plunge-corpus/aws-ts
npm ci
plunge stack init floci --secrets-provider passphrase
```

`PULUMI_BACKEND_URL` sets the backend for this shell; `plunge login` sets it
for your user.

## 5. Preview and update

```sh
plunge preview --stack floci
plunge up --stack floci
```

plunge refreshes before both, so the plan diffs against floci's live state.

## 6. See the markers and the records

```sh
plunge stack export --stack floci | grep -A1 '"plunge-stack"'
aws s3api get-bucket-tagging --bucket plc-ts-site
ls "${PULUMI_BACKEND_URL#plunge+file://}/.pulumi/records/stacks/corpus-aws-ts/floci/resources"
```

Every taggable resource carries `plunge-stack = corpus-aws-ts/floci` and its
`plunge-urn`. The bucket policy takes no tags, so its record holds its
ownership. Each URN has one record. The queues are `events-<hash>` and
`worker-0-<hash>` on every run ([Names](../../how-it-works/names/)).

After the update plunge lists each marked type and warns about marked objects
with no record. On floci it can wrongly warn about recorded queues by ID; the
fix waits for its run ([#28](https://github.com/INTENTIUS/plunge/issues/28)).

## 7. Read one resource's drift

Change a tag behind plunge's back, then read it:

```sh
aws s3api put-bucket-tagging --bucket plc-ts-docs-assets --tagging \
  "$(aws s3api get-bucket-tagging --bucket plc-ts-docs-assets | sed 's/"plunge"/"hotfix"/')"
plunge drift --stack floci --stream \
  --resource 'urn:pulumi:floci::corpus-aws-ts::corpus:index:Site$aws:s3/bucket:Bucket::docs-assets'
```

The `drift-record` line says `drifted` and shows `tags["corpus-owner"]`
before and after ([Drift of one resource](../../how-it-works/drift/)).

## 8. Clean up

```sh
plunge destroy --stack floci
docker stop plunge-floci
```
