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
It also turns off pulumi-gcp's region validation: the provider looks up the
project's regions when it configures, and that lookup ignores the custom
endpoints, so without the switch it went to compute.googleapis.com and came
back 401 (INTENTIUS/plunge#42). `gcp:skipRegionValidation: true` in the
stack file covers the default provider. The explicit `europe` provider can't
take it as a property, so it reads `PULUMI_GCP_SKIP_REGION_VALIDATION=true`
from the environment, which `scripts/plunge-baseline.sh` exports; export it
yourself for any other run of `gcp-yaml` (the by-hand steps below, an `up`).

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
service's list, and Asset Inventory, has no endpoint there, so plunge skips
those listings with a warning naming the stack and makes no call to Google.
`azure-yaml`'s ARM listings go to floci-az through its metadata host, signed
in with the fake client secret from `ARM_TENANT_ID`, `ARM_CLIENT_ID` and
`ARM_CLIENT_SECRET` in the environment (`scripts/plunge-baseline.sh` exports
them); without those three they are skipped
with the same warning, and nothing calls Azure.

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

## Running against the emulators

Two scripts make a run repeatable: one starts the emulators at fixed
images, the other runs a command with no real cloud credentials in reach
and checks afterwards that nothing it ran talked to a real cloud.

`tests/plunge-corpus/emulator-images` pins the three images by digest. They
are copies of choudoufu's `live/floci-image`, `live/floci-gcp-image` and
`live/floci-az-image`, so both repos measure against the same emulators.
Check them against a choudoufu checkout before a run, and after either side
moves a pin:

```sh
scripts/check-emulator-images.sh ../choudoufu origin/integration/consolidation-graph
# last line: emulator-images: SAME (3 images)
```

Start them, check them, stop them:

```sh
scripts/plunge-emulators.sh up        # floci 4566, floci-gcp 4588, floci-az 4577; waits until each is ready
scripts/plunge-emulators.sh status
scripts/plunge-emulators.sh down
```

Name some to act on only those (`up floci-az`). The containers are
`plunge-floci`, `plunge-floci-gcp` and `plunge-floci-az`, published on
127.0.0.1 only. `PLUNGE_FLOCI_NAME`, `PLUNGE_FLOCI_PORT` and the `_GCP_`
and `_AZ_` variants move them; the stack files name the default ports, so
a run on other ports also needs the stack file edits in "How the stacks
reach the emulators". floci-gcp gets
`FLOCI_GCP_DEFAULT_PROJECT_ID=plc-corpus`. floci-az runs with TLS and
`FLOCI_AZ_STORAGE_SUFFIX=localhost:<port>`, serving the certificate in
`FLOCI_AZ_TLS_DIR` (default `~/.config/choudoufu/floci-az-tls`, made once
by choudoufu's `scripts/floci-az-mac-cert.sh` and trusted in the keychain).
On macOS that certificate is required; on Linux, without one, floci-az
makes its own CA and `up` saves it and prints the path. `up` prints the
`FLOCI_AZ_HOST` and `FLOCI_AZ_CA_FILE` to export for the Azure program.

Each container the script starts carries the label
`io.intentius.plunge-emulators=<emulator>`. `up` reuses a running
container only when it has that label, runs the pinned image and is
published on the expected port; `down` stops only a labelled one. A
container of the same name started any other way, by hand or by another
harness, is refused by name and left running.

Then run the command through `scripts/plunge-safe-run.sh`:

```sh
scripts/plunge-safe-run.sh [--log FILE] [--scan DIR]... [--azure-fakes] [-e NAME=VALUE]... -- <command...>
```

It unsets every `AWS_*`, `AZURE_*`, `ARM_*`, `GOOGLE_*`, `CLOUDSDK_*` and
`GCE_*` variable (`AWS_PROFILE` and `GOOGLE_APPLICATION_CREDENTIALS`
included), then sets `test`/`test` static AWS keys, points
`AWS_CONFIG_FILE`, `AWS_SHARED_CREDENTIALS_FILE`, `CLOUDSDK_CONFIG` and
`AZURE_CONFIG_DIR` at empty files and directories in a scratch directory,
unsets `PULUMI_ACCESS_TOKEN` and sets `PULUMI_SKIP_UPDATE_CHECK=true`. A
`PULUMI_BACKEND_URL` you give is kept only when it is local (`file://`,
`plunge+file://`, or a plunge bucket store on a loopback emulator);
without one the run gets a scratch `file://` backend. `-e` exports a
variable after the scrub, and `--azure-fakes` exports floci-az's fake ARM
identity.

The output is teed to a log (`--log`, default `<scratch>/run.log`). After
the command exits, the log and every `--scan` directory are searched for
`UnrecognizedClientException`, `InvalidClientTokenId`, `invalid_grant`,
`AADSTS`, `CREDENTIALS_MISSING` and a 401 on a line naming
`googleapis.com`, `amazonaws.com`, `azure.com` or `windows.net`. An
emulator never answers any of those, so a hit means a call left this
machine: the script prints the lines, ends with `plunge-safe-run:
REAL-CLOUD CALL` and exits 3 whatever the command returned. Treat that as a
stop: find the endpoint that wasn't pointed at an emulator, and don't use
the run's results. A clean run ends with `plunge-safe-run: CLEAN` and the
command's own exit status, and each `--scan` directory gets a
`safe-run-verdict.txt` reading `CLEAN`.

`--scan` is for commands that write their output to files rather than the
terminal: `scripts/plunge-baseline.sh` writes each engine's stderr under its
`OUT`, so its runs pass `--scan "$OUT"`.

### The store conformance legs

`pkg/backend/plunge/conformance_test.go` runs choudoufu's record-store suite
against each plunge store. The `plunge+file://` leg always runs; the bucket
legs skip unless their variable names a store on a loopback emulator:

| Variable | Emulator | Also needs |
|---|---|---|
| `PLUNGE_CONFORMANCE_S3_URL` | floci | the static AWS keys (plunge-safe-run sets them) |
| `PLUNGE_CONFORMANCE_GS_URL` | floci-gcp | `STORAGE_EMULATOR_HOST=localhost:4588` |
| `PLUNGE_CONFORMANCE_AZBLOB_URL` | floci-az | `ARM_TENANT_ID`, `ARM_CLIENT_ID`, `ARM_CLIENT_SECRET` (`--azure-fakes`), and floci-az's CA trusted (`SSL_CERT_FILE` on Linux, the keychain on macOS) |

Opening a bucket store checks its settings (INTENTIUS/plunge#20), so each
bucket or container needs versioning, a lifecycle rule deleting noncurrent
versions, and its public access settings closed, the same settings
`secure_bucket` in `scripts/plunge-races/lib.sh` puts on each race's S3
bucket. That file runs its floci checks when sourced, so here is the
setup for the three stores, run once per emulator start (each starts
empty):

```sh
# floci S3 (aws CLI; the keys are floci's)
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
S3="--endpoint-url http://localhost:4566"
aws $S3 s3api create-bucket --bucket plunge-conformance
aws $S3 s3api put-bucket-versioning --bucket plunge-conformance --versioning-configuration Status=Enabled
aws $S3 s3api put-bucket-lifecycle-configuration --bucket plunge-conformance --lifecycle-configuration \
  '{"Rules":[{"ID":"records-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}'
aws $S3 s3api put-public-access-block --bucket plunge-conformance --public-access-block-configuration \
  BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true

# floci-gcp GCS (its JSON API, no credentials)
curl -fsS -X POST -H 'Content-Type: application/json' \
  'http://localhost:4588/storage/v1/b?project=plc-corpus' -d '{"name":"plunge-conformance",
   "versioning":{"enabled":true},
   "lifecycle":{"rule":[{"action":{"type":"Delete"},"condition":{"daysSinceNoncurrentTime":30}}]},
   "iamConfiguration":{"uniformBucketLevelAccess":{"enabled":true},"publicAccessPrevention":"enforced"}}'

# floci-az (ARM over TLS, with a token floci-az issues to the fake client)
CA="$FLOCI_AZ_CA_FILE"; AZ=https://localhost:4577
SUB=00000000-0000-0000-0000-00000000c0de; TENANT=00000000-0000-0000-0000-0000000071e7
TOKEN="$(curl -fsS --cacert "$CA" "$AZ/$TENANT/oauth2/v2.0/token" -d grant_type=client_credentials \
  -d client_id=00000000-0000-0000-0000-00000000c11e -d client_secret=floci-az-does-not-check-this \
  -d scope=https://management.azure.com/.default | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')"
arm() { curl -fsS --cacert "$CA" -X PUT -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$AZ$1" -d "$2"; echo; }
RG=/subscriptions/$SUB/resourceGroups/plunge-conformance-rg
ACCT=$RG/providers/Microsoft.Storage/storageAccounts/plungeconf
arm "$RG?api-version=2021-04-01" '{"location":"eastus"}'
arm "$ACCT?api-version=2023-05-01" '{"location":"eastus","kind":"StorageV2","sku":{"name":"Standard_LRS"},"properties":{"allowBlobPublicAccess":false}}'
arm "$ACCT/blobServices/default?api-version=2023-05-01" '{"properties":{"isVersioningEnabled":true}}'
arm "$ACCT/managementPolicies/default?api-version=2023-05-01" '{"properties":{"policy":{"rules":[{"enabled":true,"name":"records-noncurrent","type":"Lifecycle","definition":{"actions":{"version":{"delete":{"daysAfterCreationGreaterThan":30}}},"filters":{"blobTypes":["blockBlob"]}}}]}}}'
arm "$ACCT/blobServices/default/containers/records?api-version=2023-05-01" '{"properties":{"publicAccess":"None"}}'
```

Then the legs, each through the wrapper:

```sh
LOGS="$(mktemp -d)"
scripts/plunge-safe-run.sh --log "$LOGS/conformance-s3.log" \
  -e 'PLUNGE_CONFORMANCE_S3_URL=plunge+s3://plunge-conformance/conf?endpoint=http://localhost:4566&region=us-east-1&s3ForcePathStyle=true' \
  -- go test -C pkg -count=1 -run 'TestPlungeS3StoreConformance' -v ./backend/plunge/
scripts/plunge-safe-run.sh --log "$LOGS/conformance-gs.log" \
  -e STORAGE_EMULATOR_HOST=localhost:4588 -e PLUNGE_CONFORMANCE_GS_URL=plunge+gs://plunge-conformance/conf \
  -- go test -C pkg -count=1 -run 'TestPlungeGSStoreConformance' -v ./backend/plunge/
scripts/plunge-safe-run.sh --log "$LOGS/conformance-azblob.log" --azure-fakes -e "SSL_CERT_FILE=$FLOCI_AZ_CA_FILE" \
  -e 'PLUNGE_CONFORMANCE_AZBLOB_URL=plunge+azblob://records/conf?storage_account=plungeconf&resource_group=plunge-conformance-rg&subscription_id=00000000-0000-0000-0000-00000000c0de&metadata_host=localhost:4577' \
  -- go test -C pkg -count=1 -run 'TestPlungeAzblobStoreConformance' -v ./backend/plunge/
```

Read each leg's `--- PASS`/`--- SKIP` lines as well as the wrapper's last
line: a leg that skipped didn't run. Each leg refuses, before opening
anything, a URL whose endpoint isn't loopback.

## Re-recording the baseline

The committed baseline is each program's `baseline/preview.json` (stock
`pulumi preview --json`), `baseline/plunge-compare.txt` (plunge against
stock), `baseline/pulumi-version.txt`, and `baseline-verdicts.txt`, one
`VERDICT` line per program. Re-record it after any change that can move a
preview:

- a corpus change: a program, its stack file or its dependency pins;
- a provider change: a pulumi-aws, pulumi-gcp or pulumi-azure version;
- a stock pulumi change (the fork point moves);
- an emulator pin change in `emulator-images`.

Re-record only the programs the change touches. A plunge-only change
needs no re-record: run `scripts/plunge-baseline.sh` and compare its
verdicts with `baseline-verdicts.txt`.

`plunge-baseline-record.sh` records every program in the run's
`verdicts.txt`, so name only the touched programs to `plunge-baseline.sh`
for a recording run. An untouched program never reproduces its committed
`preview.json` byte for byte: the autonamed suffixes are random, and the
order of the steps and of a property's dependency URNs follows the order the
language host registered them in, which varies between runs. What does
reproduce is its `plunge-compare.txt`, its `VERDICT` line, and the preview
with steps sorted by URN, dependency lists sorted and autonamed suffixes
masked (2026-10-27, all six untouched programs, INTENTIUS/plunge#42).

Baseline files are never hand-edited. They are copied from one run by
`scripts/plunge-baseline-record.sh`, which refuses a run the safe-run
check didn't pass, and a program whose verdict is `ERROR` or
`DIFF-STEPS`. If a recorded file looks wrong, fix the program, the
provider or the engine and record again.

```sh
scripts/check-emulator-images.sh ../choudoufu origin/integration/consolidation-graph
scripts/plunge-emulators.sh up
export OUT="$PWD/plunge-baseline-out/$(date -u +%Y%m%dT%H%M%SZ)" PULUMI=/path/to/stock/pulumi
mkdir -p "$OUT"
scripts/plunge-safe-run.sh --log "$OUT/safe-run.log" --scan "$OUT" -- scripts/plunge-baseline.sh [program ...]
#   last line: plunge-safe-run: CLEAN (command exited 0; ...)
scripts/plunge-baseline-record.sh "$OUT"
scripts/plunge-emulators.sh down
git diff --stat -- tests/plunge-corpus
```

`plunge-baseline.sh` does its own scrub as well (it unsets the cloud
variables, uses its own file backends and floci-az's fakes), so the
wrapper's part is the empty config locations and the log check. Read
`$OUT/verdicts.txt` and each `compare.txt` before committing, and the
"Recording the stock baseline" checks below (a good baseline has no
`error` diagnostics and the expected `create` steps). `DIFF-INPUTS` is
expected where a resource is autonamed: the random suffix differs between
the two engines' previews. Commit the baseline files together with the
change that made them move, and say in the commit message which run
produced them: the images, the stock pulumi and plunge versions (from
`$OUT/versions.txt`) and the `plunge-safe-run: CLEAN` line.

## Recording the stock baseline

The maintainer runs this by hand. A baseline is stock `pulumi preview`
against an empty stack on a local file backend, with floci running. Never
Pulumi Cloud.

`scripts/plunge-baseline.sh` (P-L0) does steps 2 and 3 for stock pulumi and
plunge side by side, each on its own copy of the program and its own file
backend, and writes a `VERDICT <program> ...` line per program. The
committed `baseline/` comes from that script's run, copied by
`scripts/plunge-baseline-record.sh` ("Re-recording the baseline" above).
The steps below are the stock half by hand, for looking into one program's
preview; what they write is not committed.

1. Start the emulators at the pinned images (above, "Running against the
   emulators"):

   ```sh
   scripts/plunge-emulators.sh up
   ```

   On macOS, floci-az needs the certificate from choudoufu's
   `scripts/floci-az-mac-cert.sh`, trusted in the keychain, since Go there
   ignores `SSL_CERT_FILE`; pass it as
   `FLOCI_AZ_CA_FILE=~/.config/choudoufu/floci-az-tls/cert.pem`. On Linux
   `scripts/plunge-baseline.sh` fetches floci-az's CA from
   `http://localhost:4577/_floci/tls-cert` and exports it as
   `SSL_CERT_FILE` for both engines.

2. Set up a throwaway state directory and the shared passphrase, and clear
   anything that could reach a real cloud:

   ```sh
   export STATE="$(mktemp -d)"
   pulumi login "file://$STATE"
   export PULUMI_CONFIG_PASSPHRASE=plunge-baseline   # scripts/plunge-baseline.sh's default
   export PULUMI_SKIP_UPDATE_CHECK=true
   unset AWS_PROFILE AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
   unset GOOGLE_APPLICATION_CREDENTIALS GOOGLE_OAUTH_ACCESS_TOKEN
   export PULUMI_GCP_SKIP_REGION_VALIDATION=true   # gcp-yaml's europe provider (see above)
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
   # the stack's fakes, which plunge's ARM lister reads from the environment only
   export ARM_TENANT_ID=00000000-0000-0000-0000-0000000071e7 ARM_CLIENT_ID=00000000-0000-0000-0000-00000000c11e
   export ARM_CLIENT_SECRET=floci-az-does-not-check-this
   ```

   `pulumi login` changes the CLI's current backend for the whole user. To
   keep it to this shell, `export PULUMI_BACKEND_URL="file://$STATE"`
   instead.

3. For each program, install, create the stack and record:

   ```sh
   cd tests/plunge-corpus/<program>
   # install: npm ci | pulumi install | go mod download | dotnet restore | nothing for YAML
   pulumi stack init floci --secrets-provider passphrase
   pulumi version > "$STATE/pulumi-version.txt"
   pulumi preview --stack floci --json > "$STATE/preview.json"
   ```

   `pulumi stack init` writes an `encryptionsalt` into `Pulumi.floci.yaml`
   when the file has none. The committed stack files already carry one, so
   later runs (P-L0's plunge-vs-stock comparison) decrypt with the same
   passphrase and salt; a new program commits its salt with its first
   baseline.

4. Read the result. A good baseline has no
   `diagnostics` of severity `error`, has a `create` step for every resource
   in the table above except `vpc-id`, shows `vpcId`, `securityGroupId` and
   `vpcParameterName` as unknown, and shows `dbPasswordValue` as a secret. The
   exit code alone isn't the verdict.

   For `azure-yaml`, a good baseline creates `app-rg`, `scratch-rg`,
   `app-vnet`, `app-subnet`, `app-nsg`, the `west` provider and `west-rg`,
   with the IDs unknown, and in the plunge comparison only `scratch-rg`'s
   autonamed name may differ (`DIFF-INPUTS`).

Autonamed resources (`events`, `worker-*`, `docs-notify`, `app-sg`'s name,
`azure-yaml`'s `scratch-rg`)
get a random suffix on every preview, so two baselines differ there even
when nothing else does. A comparison masks those fields.

For adoption work (P-L4) a second artifact helps: after `pulumi up --stack
floci --yes` against floci, `pulumi stack export --stack floci >
baseline/stack-export.json` and `pulumi preview --refresh --stack floci
--json > baseline/preview-refresh.json`. That one applies to the emulator,
so it's the maintainer's call when to record it.
