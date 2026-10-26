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
| `azure-yaml` | Pulumi YAML | floci-az | pulumi-azure 6.10.0 |

The five `aws-*` programs declare the same resources under the same logical
names, so their previews line up step by step and a difference between
languages is a difference in the language host or SDK, not in the program.
YAML can't define a component resource, can't create a resource inside an
apply, and has no loop, so `aws-yaml` leaves out `docs`, `vpc-id` and the
loop (its three worker queues are written out). Physical names carry a
per-program prefix (`plc-ts`, `plc-go`, ...) so all five can run against one
floci without colliding.

`azure-yaml` uses pulumi-azure (Azure classic, bridged from azurerm), as
ruled on INTENTIUS/plunge#16, so all three clouds go through the same bridged
engine path. azure-native can follow once plunge reads its schema for
markers (INTENTIUS/plunge#15).

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

`azure-yaml` has a client-named resource group (`app-rg`) and an autonamed
one (`scratch-rg`, no name, so the engine adds a random suffix), both with
map `tags`; a virtual network and a network security group inside `app-rg`,
client-named with `tags`, whose IDs are ARM resource IDs built from the group
and the name; a subnet (untaggable, no `tags` argument); and an explicit
provider (`west`) with its own floci-az wiring and a resource group through
it. Like `gcp-yaml`, its first purpose is to find out whether pulumi-azure runs
against floci-az at all.

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

`azure-yaml` sets `azure:metadataHost` to `localhost:4577`. With a metadata
host set, azurerm reads every endpoint (ARM, the Entra login endpoint, the
storage suffix) from floci-az's `/metadata/endpoints`, over TLS. The
subscription, tenant and client IDs and the client secret are fakes (floci-az
issues a token for any client), `useCli`, `useMsi`, `useOidc` and
`useAksWorkloadIdentity` are off so nothing falls back to an `az login`, and
`resourceProviderRegistrations: none`, since floci-az answers the providers
listing with an empty list. This is the wiring choudoufu's
`live/e2e/lib/floci-az.sh` gives hashicorp/azurerm.

plunge's own marker listings follow the same configuration
(docs/plunge/CONCURRENCY.md, "Where the listings call"). The `aws-*` stacks
give every service one URL, so Cloud Control is listed there with
`PLUNGE_CLOUDCONTROL_ENDPOINT` unset; set the variable to send it somewhere
else. In `gcp-yaml` the storage and pubsub lists go to floci-gcp; any other
service's list, and Asset Inventory, has no endpoint there, and `azure-yaml`
reads its endpoints from a metadata host, so plunge skips those listings with
a warning naming the stack and makes no call to Google or Azure.

If an emulator listens on another port, change it in the stack file (both
the provider endpoints and the `flociEndpoint` key; for `azure-yaml`,
`azure:metadataHost` and `flociAzHost`, and `FLOCI_AZ_HOST` below).

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

`scripts/plunge-baseline.sh` (P-L0) does steps 2 and 3 for stock pulumi and
plunge side by side, each on its own copy of the program and its own file
backend, and writes a `VERDICT <program> ...` line per program. The steps
below are the stock half by hand, for recording `baseline/` in the tree.

1. Start the emulators at choudoufu's pinned images:

   ```sh
   CHDF=/path/to/choudoufu
   docker run -d --rm --name plunge-floci -p 127.0.0.1:4566:4566 "$(cat $CHDF/live/floci-image)"
   docker run -d --rm --name plunge-floci-gcp -p 127.0.0.1:4588:4588 \
     -e FLOCI_GCP_DEFAULT_PROJECT_ID=plc-corpus "$(cat $CHDF/live/floci-gcp-image)"
   docker run -d --rm --name plunge-floci-az -p 127.0.0.1:4577:4577 \
     -e FLOCI_AZ_TLS_ENABLED=true -e FLOCI_AZ_STORAGE_MODE=memory -e FLOCI_AZ_PARITY=azure \
     -e FLOCI_AZ_STORAGE_SUFFIX=localhost:4577 "$(cat $CHDF/live/floci-az-image)"
   ```

   floci-az generates its own CA when it starts with no certificate given.
   On Linux that CA is enough: `scripts/plunge-baseline.sh` fetches it from
   `http://localhost:4577/_floci/tls-cert` and exports it as `SSL_CERT_FILE`
   for both engines. On macOS, Go ignores `SSL_CERT_FILE` and trusts only the
   keychain, so make a certificate once with choudoufu's
   `scripts/floci-az-mac-cert.sh`, trust it, and start floci-az with it
   mounted (`-v ~/.config/choudoufu/floci-az-tls:/etc/floci-az-tls:ro -e
   FLOCI_AZ_TLS_CERT_PATH=/etc/floci-az-tls/cert.pem -e
   FLOCI_AZ_TLS_KEY_PATH=/etc/floci-az-tls/key.pem`), then pass
   `FLOCI_AZ_CA_FILE=~/.config/choudoufu/floci-az-tls/cert.pem`.

2. Set up a throwaway state directory and the shared passphrase, and clear
   anything that could reach a real cloud:

   ```sh
   export STATE="$(mktemp -d)"
   pulumi login "file://$STATE"
   export PULUMI_CONFIG_PASSPHRASE=plunge-baseline   # scripts/plunge-baseline.sh's default
   export PULUMI_SKIP_UPDATE_CHECK=true
   unset AWS_PROFILE AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
   unset GOOGLE_APPLICATION_CREDENTIALS GOOGLE_OAUTH_ACCESS_TOKEN
   export AWS_ENDPOINT_URL=http://localhost:4566   # a second guard behind aws:endpoints
   ```

   For `azure-yaml`, also clear the real Azure credentials and give the
   provider floci-az's CA and fakes, as `scripts/plunge-baseline.sh` does:

   ```sh
   for v in $(env | sed -n 's/^\(AZURE_[A-Z_]*\)=.*/\1/p;s/^\(ARM_[A-Z_]*\)=.*/\1/p'); do unset "$v"; done
   curl -sf http://localhost:4577/_floci/tls-cert -o /tmp/floci-az-ca.pem   # or the macOS cert above
   export SSL_CERT_FILE=/tmp/floci-az-ca.pem
   export AZURE_CONFIG_DIR="$(mktemp -d)"   # no az login to fall back on
   export ARM_METADATA_HOSTNAME=localhost:4577 ARM_USE_CLI=false ARM_USE_MSI=false ARM_USE_OIDC=false
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

   For `azure-yaml`, a good baseline creates `app-rg`, `scratch-rg`,
   `app-vnet`, `app-subnet`, `app-nsg`, the `west` provider and `west-rg`,
   with the IDs unknown. Then run `scripts/plunge-baseline.sh azure-yaml`,
   copy its `azure-yaml/compare.txt` to `baseline/plunge-compare.txt`, and add
   its `VERDICT azure-yaml ...` line to `baseline-verdicts.txt`. Only
   `scratch-rg`'s autonamed name may differ (`DIFF-INPUTS`).

Autonamed resources (`events`, `worker-*`, `docs-notify`, `app-sg`'s name,
`azure-yaml`'s `scratch-rg`)
get a random suffix on every preview, so two baselines differ there even
when nothing else does. A comparison masks those fields.

For adoption work (P-L4) a second artifact helps: after `pulumi up --stack
floci --yes` against floci, `pulumi stack export --stack floci >
baseline/stack-export.json` and `pulumi preview --refresh --stack floci
--json > baseline/preview-refresh.json`. That one applies to the emulator,
so it's the maintainer's call when to record it.
