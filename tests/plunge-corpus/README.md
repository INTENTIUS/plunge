# plunge corpus

Pulumi programs that plunge is measured against (INTENTIUS/plunge#4, lane L1
of the design record INTENTIUS/plunge#9). Each directory is one program with
one stack, `floci`, whose config points every provider call at an emulator.
None of them touches a real cloud. P-L0 compares `plunge preview` with stock
`pulumi preview` on these programs, and P-L3 to P-L5 use them for the
concurrency claims C1 to C6 (the parity table on INTENTIUS/plunge#2) and for
adoption.

`corpus.json` lists the programs with their install and compile commands, for
scripts. This file is for people.

## Programs

| Directory | Language | Emulator | Provider |
|---|---|---|---|
| `aws-ts` | TypeScript | floci | pulumi-aws 7.51.1 |
| `aws-python` | Python | floci | pulumi-aws 7.51.1 |
| `aws-go` | Go | floci | pulumi-aws 7.51.1 |
| `aws-yaml` | Pulumi YAML | floci | pulumi-aws 7.51.1 |
| `aws-csharp` | C# | floci | pulumi-aws 7.51.1 |
| `gcp-yaml` | Pulumi YAML | floci-gcp | pulumi-gcp 10.2.0 |

The five `aws-*` programs declare the same resources under the same logical
names, so their previews line up step by step and a difference between
languages is a difference in the language host or SDK, not in the program.
YAML can't define a component resource, can't create a resource inside an
apply, and has no loop, so `aws-yaml` leaves out `docs`, `vpc-id` and the
loop (its three worker queues are written out). Physical names carry a
per-program prefix (`plc-ts`, `plc-go`, ...) so all five can run against one
floci without colliding.

No Azure program yet. Whether azure-native or azure classic run against
floci-az is unknown, and floci-az needs TLS and a metadata host
(choudoufu's `live/e2e/lib/floci-az.sh`), which is more wiring than this
first cut takes on.

## What each resource exercises

Logical names are the same in every `aws-*` program.

| Logical name | Type | Exercises |
|---|---|---|
| `site-bucket` | `aws:s3:Bucket`, `bucket: <prefix>-site` | Client-named (C1): two creates collide on S3's uniqueness constraint. Map `tags`. |
| `app-role` | `aws:iam:Role`, `name: <prefix>-app` | Client-named in IAM's account-wide namespace (C1). Map `tags`. |
| `site-bucket-policy` | `aws:s3:BucketPolicy` | Untaggable: no `tags` argument, so ownership can't ride a marker and has to sit in the record. Depends on two other resources' outputs. |
| `main` | `aws:ec2:Vpc` | Server-assigned ID (C2): nothing in the program can collide, two creates make two VPCs. `vpcId` output is unknown at a first preview. |
| `app-sg` | `aws:ec2:SecurityGroup` | Server-assigned ID (`sg-...`) with an autonamed `name`, so it sits on both sides of the autonaming decision. |
| `events` | `aws:sqs:Queue`, no name | Autonamed: the engine adds a random suffix in `Check`. Its `messageRetentionSeconds` comes from `corpus:eventsRetentionSeconds` (default 3600), the knob for C3's divergent in-place updates. |
| `worker-0` .. `worker-N` | `aws:sqs:Queue`, no name | Count-like loop over `corpus:workerCount` (default 3). Lowering the count in one run while another updates `worker-2` is C4 (update racing a destroy). Siblings written by two runs are C5's disjoint applies. |
| `docs` | `corpus:index:Site` (component) | A component with two children, `docs-assets` (client-named bucket) and `docs-notify` (autonamed topic), and registered outputs. Children carry the component in their URN parent chain. |
| `west` | `pulumi:providers:aws` | An explicit provider (us-west-2) with its own floci wiring. Every other resource uses the default provider from the stack config, so both provider kinds and their IDs land in the checkpoint. |
| `alerts-west` | `aws:sns:Topic`, `name: <prefix>-alerts-west` | Client-named through the explicit provider. |
| `db-password` | `aws:ssm:Parameter`, SecureString | A secret input. The value is `corpus:dbPassword` if set as a secret, else a literal wrapped as a secret, so the baseline doesn't need a ciphertext in the stack file. |
| `vpc-id` | `aws:ssm:Parameter`, created inside `vpc.id.apply(...)` | A resource created inside apply. Its name contains the VPC ID, so on a first preview the callback never runs and the resource is absent from the preview (design P3's "may appear"). It shows up only at `up`. |

Stack outputs: `siteBucketName`, `sitePolicyBucket`, `appRoleArn`, `vpcId`,
`securityGroupId`, `eventsQueueUrl`, `workerQueueUrls` (a list),
`docsBucketName`, `docsTopicArn`, `westAlertsArn`, `dbPasswordParameter`,
`dbPasswordValue` (secret) and `vpcParameterName` (unknown at preview, and
an output of a resource made inside apply). Under C5 each output becomes its
own record. `aws-yaml` has the same set minus the `docs*` and
`vpcParameterName` outputs.

C6 (a crash is a re-run) needs no resource of its own: kill any `up` part way
and re-run.

`gcp-yaml` is smaller. Every GCP name is client-chosen: a storage bucket and
two Pub/Sub topics and a subscription with map `labels`, a service account
(untaggable, no labels), and an explicit provider (`europe`) beside the
default one. pulumi-gcp adds its `goog-pulumi-provisioned` label by default;
the program leaves that on, since it's what a stock stack does. Its purpose
is to find out whether pulumi-gcp runs against floci-gcp at all.

## How the stacks reach the emulators

The `aws-*` stack files set the default provider: `aws:endpoints` for every
service the programs call (ec2, iam, s3, sns, sqs, ssm, sts) at
`http://localhost:4566`, `aws:accessKey`/`aws:secretKey` set to `test` (this
overrides any real credentials in the environment or `~/.aws`),
`skipCredentialsValidation`, `skipRequestingAccountId`,
`skipRegionValidation`, `skipMetadataApiCheck` and `s3UsePathStyle`. The
explicit `west` provider sets the same flags in code and reads its endpoint
from `corpus:flociEndpoint`. The YAML programs read their own project
namespace, so their keys are `corpus-aws-yaml:*` and `corpus-gcp-yaml:*`
instead of `corpus:*`.

`gcp-yaml` sets `gcp:accessToken` to a fake token and points the storage,
pubsub, iambeta, resource manager, service usage and cloud billing endpoints
at `http://localhost:4588`, with the paths choudoufu's
`live/e2e/lib/gcp.sh` uses for hashicorp/google against the same emulator.

If an emulator listens on another port, change it in the stack file (both
the provider endpoints and the `flociEndpoint` key).

## Compile checks

These are the only checks run when the corpus is changed. Nothing here runs
a program.

```sh
cd aws-ts && npm ci && npx tsc --noEmit
cd aws-go && go build -o /dev/null .
cd aws-python && python3 -m py_compile __main__.py
cd aws-csharp && dotnet build -nologo
```

The YAML programs have no compile step. `aws-go` is its own Go module, so the
repo's `tests` module doesn't build it.

## Recording the stock baseline

The maintainer runs this by hand. A baseline is stock `pulumi preview`
against an empty stack on a local file backend, with floci running. Never
Pulumi Cloud.

1. Start the emulators at choudoufu's pinned images:

   ```sh
   CHDF=/path/to/choudoufu
   docker run -d --rm --name plunge-floci -p 127.0.0.1:4566:4566 "$(cat $CHDF/live/floci-image)"
   docker run -d --rm --name plunge-floci-gcp -p 127.0.0.1:4588:4588 \
     -e FLOCI_GCP_DEFAULT_PROJECT_ID=plc-corpus "$(cat $CHDF/live/floci-gcp-image)"
   ```

2. Set up a throwaway state directory and the shared passphrase, and clear
   anything that could reach a real cloud:

   ```sh
   export STATE="$(mktemp -d)"
   pulumi login "file://$STATE"
   export PULUMI_CONFIG_PASSPHRASE=plunge-corpus
   export PULUMI_SKIP_UPDATE_CHECK=true
   unset AWS_PROFILE AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
   unset GOOGLE_APPLICATION_CREDENTIALS GOOGLE_OAUTH_ACCESS_TOKEN
   export AWS_ENDPOINT_URL=http://localhost:4566   # a second guard behind aws:endpoints
   ```

   `pulumi login` changes the CLI's current backend for the whole user. To
   keep it to this shell, `export PULUMI_BACKEND_URL="file://$STATE"`
   instead.

3. For each program, install, create the stack and record:

   ```sh
   cd tests/plunge-corpus/<program>
   # install: npm ci | pulumi install | go mod download | dotnet restore | nothing for YAML
   pulumi stack init floci --secrets-provider passphrase
   mkdir -p baseline
   pulumi version > baseline/pulumi-version.txt
   pulumi preview --stack floci --json > baseline/preview.json
   ```

   `pulumi stack init` writes an `encryptionsalt` into `Pulumi.floci.yaml`.
   Commit it with the baseline, so later runs (P-L0's plunge-vs-stock
   comparison) decrypt with the same passphrase and salt.

4. Read the result before committing it. A good baseline has no
   `diagnostics` of severity `error`, has a `create` step for every resource
   in the table above except `vpc-id`, shows `vpcId`, `securityGroupId` and
   `vpcParameterName` as unknown, and shows `dbPasswordValue` as a secret. The
   exit code alone isn't the verdict.

Autonamed resources (`events`, `worker-*`, `docs-notify`, `app-sg`'s name)
get a random suffix on every preview, so two baselines differ there even
when nothing else does. A comparison masks those fields.

For adoption work (P-L4) a second artifact helps: after `pulumi up --stack
floci --yes` against floci, `pulumi stack export --stack floci >
baseline/stack-export.json` and `pulumi preview --refresh --stack floci
--json > baseline/preview-refresh.json`. That one applies to the emulator,
so it's the maintainer's call when to record it.
