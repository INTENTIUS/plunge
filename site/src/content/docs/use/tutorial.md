---
title: Tutorial
description: Run the aws-ts corpus program under plunge on the floci emulator, see the markers and the records, and read one resource's drift.
prompt: |
  Follow https://intentius.io/plunge/use/tutorial/ up to step 5. Install plunge, start floci, log in to the plunge+file:// backend and run `plunge preview --stack floci`, then report the preview. I run `plunge up` myself.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---

This tutorial runs the `aws-ts` corpus program under plunge on
[floci](../../glossary/#floci), a local AWS emulator, so nothing reaches AWS.
For a first look without Docker, the
[quickstart](../start/#quickstart) takes five minutes.

:::caution[How it ends]
Step 8's `plunge destroy` is refused, and that is expected. floci drops the
tags of the program's two SSM parameters
([lex00/floci#263](https://github.com/lex00/floci/issues/263)), and plunge
refuses a delete when a taggable object has lost its markers. Cleaning up
means stopping floci and deleting the temporary record directory.
:::

You need:

- plunge on your `PATH` ([Quickstart](../start/#quickstart));
- git, to clone the repository for the corpus program and the emulator script;
- Docker, to run floci;
- Node.js, for the TypeScript program;
- the AWS CLI v2.13 or later, which reads `AWS_ENDPOINT_URL`.

## 1. Get the corpus

```sh
git clone --depth 1 https://github.com/INTENTIUS/plunge
cd plunge
plunge version
```

## 2. Start floci

`scripts/plunge-emulators.sh` starts the floci image pinned in
`tests/plunge-corpus/emulator-images`, on `127.0.0.1:4566`:

```sh
scripts/plunge-emulators.sh up floci
```

## 3. Keep the shell away from AWS

```sh
unset AWS_PROFILE AWS_SESSION_TOKEN
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
export AWS_ENDPOINT_URL=http://localhost:4566
export PULUMI_CONFIG_PASSPHRASE=plunge-baseline
export PULUMI_SKIP_UPDATE_CHECK=true
export AWS_DEFAULT_OUTPUT=json
```

The stack file `tests/plunge-corpus/aws-ts/Pulumi.floci.yaml` points every
provider call at floci with fake keys, and the `aws` commands below use the
same fake keys. `AWS_DEFAULT_OUTPUT=json` keeps the CLI's output JSON, which
the `sed` in step 7 edits.

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
with no record. Since [#28](https://github.com/INTENTIUS/plunge/issues/28) it
matches recorded queues and Lambda functions by their other names.

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
plunge destroy --stack floci        # refused on the two SSM parameters; deletes nothing
cd ../../..
scripts/plunge-emulators.sh down floci
rm -rf "${PULUMI_BACKEND_URL#plunge+file://}"
unset PULUMI_BACKEND_URL
```

The destroy is refused before it deletes anything: floci drops the SSM
parameters' tags on create
([lex00/floci#263](https://github.com/lex00/floci/issues/263)), so the live
marker check finds none ([Deletes](../../how-it-works/deletes/)). Stopping
floci discards every object it holds, and the last two lines delete the
records and leave the shell's backend as it was.
