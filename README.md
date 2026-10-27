# plunge

Pulumi with per-resource ownership: marker tags and a per-resource record store instead of one locked
checkpoint. plunge is to Pulumi what choudoufu is to Terraform: the engine tier for Pulumi teams on
chant. The plan is in INTENTIUS/plunge#2 and the design record in INTENTIUS/plunge#9.

At this stage (P-L0, INTENTIUS/plunge#3) plunge is stock Pulumi under another name. It runs the same
programs, SDKs, language hosts and providers. The state backend rewrite comes in later milestones.

## Fork point

Forked from [pulumi/pulumi](https://github.com/pulumi/pulumi) master at `7e12743da8` (2026-10-25,
after the 3.272.0 release), with upstream history kept. The `upstream` remote points at pulumi/pulumi
and upstream is merged in, never rebased.

Choices made to keep those merges cheap:

- Go module paths stay `github.com/pulumi/pulumi/...`, and the CLI's main package stays at
  `pkg/cmd/pulumi`. Only the output name changes, to `plunge`.
- plunge-only code goes in new files where it can: `pkg/plunge/`, `build/plunge.mk`,
  `.goreleaser.plunge.yml`, `scripts/plunge-*.sh`. Upstream's `.goreleaser.yml` and Makefile targets
  are left in place.
- Plugin protocol names are unchanged. Language hosts are still `pulumi-language-<lang>` and providers
  `pulumi-resource-<name>`, because SDKs and providers look them up by those names.
- Upstream's GitHub workflows are kept, inactive, in `.github/upstream-workflows/`. See the README there.

## Versions

A plunge build carries two versions:

- plunge's own, from `.plunge-version` (or `PLUNGE_VERSION`), and
- the Pulumi-compatible version, the newest upstream release merged in (the top heading of
  `CHANGELOG.md`, or `PULUMI_VERSION`). This is what the engine reports wherever upstream reports
  "the CLI version": a project's `requiredPulumiVersion`, a plugin's `pulumiVersionRange`, and the
  `RequirePulumiVersion` RPC that SDKs call (`pulumi.requirePulumiVersion` and friends).

`scripts/plunge-version.sh plunge|pulumi|ldflags` prints them.

```console
$ plunge version
plunge v0.1.0-dev.0
pulumi v3.272.0
$ plunge version --pulumi-compatible
v3.272.0
```

Invoked through a symlink named `pulumi` (for Automation API, which runs `pulumi version` and parses
a bare semver), `version` prints only the Pulumi-compatible version.

plunge doesn't run upstream's update check, which would point at pulumi's releases and installers.

The generated Automation API wrappers (`sdk/go/auto/automation`, `sdk/nodejs/automation/interface`,
`sdk/python/lib/pulumi/automation/interface`) are upstream's, regenerated on each upstream merge.
plunge's own commands, `drift` and `stack adopt`, are in hand-written files beside them: Go's
`API.Drift` and `API.StackAdopt` in `sdk/go/auto/automation/plunge_commands.go` (options in
`optplungedrift` and `optplungestackadopt`), and a `PlungeAPI` class, the generated API plus `drift`
and `stackAdopt` / `stack_adopt`, in `sdk/nodejs/automation/plunge.ts` and
`sdk/python/lib/pulumi/automation/plunge.py`.

## Building

Tools come from `.mise.toml`, as upstream (see `CLAUDE.md`).

```sh
make plunge                  # bin/plunge, both versions stamped
make build                   # upstream's build (CLI, SDKs, language hosts) plus bin/plunge
make plunge_language_hosts   # fetch the bundled dotnet and yaml hosts into bin/ for this machine
mise run plunge-build        # bin/plunge without make
```

`make plunge` needs mise's protoc. On a fresh checkout it first runs `proto/generate.sh` (the
`.make/proto` step), which looks protoc up with `mise where protoc` and also needs mise's
protoc-gen-go, protoc-gen-go-grpc and the node protoc plugins. The generated protobuf code is
committed, so building the binary itself doesn't need protoc. Without mise, build it directly:

```sh
go build -C pkg -o ../bin/plunge -ldflags "$(./scripts/plunge-version.sh ldflags)" ./cmd/pulumi
```

Dropping the `-ldflags` leaves both versions empty, which turns the version checks off, as on an
upstream dev build.

The language hosts are separate Go modules and build with a plain `go build`, for example
`go build -C sdk/nodejs/cmd/pulumi-language-nodejs -o ../../../../bin/pulumi-language-nodejs .`.
Build to `bin/` (ignored) or to `-o /dev/null`. A `go build` with no `-o` leaves the binary in the
source directory; `.gitignore` covers those paths so one isn't committed by accident.

## Language hosts

The release archive is laid out like upstream's. It bundles the in-repo hosts (nodejs, bun, python,
go, pcl) built from this tree, plus the dotnet and yaml hosts at the versions pinned in
`scripts/get-language-providers.sh`, downloaded from their pulumi releases. Java and HCL are not
bundled: the CLI installs them on first use at the versions pinned in `pkg/util/plugin.go`
(`knownLanguageRuntimes`). The bundled hosts sit next to the `plunge` binary, which is where the CLI
looks for them.

A host that is neither bundled nor on `PATH` is fetched the way upstream fetches it: stock pulumi's
build of that host, at plunge's Pulumi-compatible version, not one built from this tree. That is the
same code while plunge's hosts don't diverge from upstream's.

Releases are built by `.github/workflows/plunge-release.yml` when a `vX.Y.Z` tag is pushed. It runs
goreleaser with `.goreleaser.plunge.yml` and publishes `plunge-vX.Y.Z-<os>-<arch>` archives on this
repository's releases page. Windows binaries are unsigned. The snapshot build below shows the same
artifact list without publishing anything.

```sh
./scripts/prep-for-goreleaser.sh
PULUMI_VERSION=$(./scripts/plunge-version.sh pulumi) PLUNGE_VERSION=$(./scripts/plunge-version.sh plunge) \
  goreleaser release -f .goreleaser.plunge.yml --snapshot --clean
```

## Baseline against stock Pulumi

`scripts/plunge-baseline.sh` runs `plunge preview` and stock `pulumi preview` on each program in
`tests/plunge-corpus/` against the local emulators and compares the two. The acceptance for P-L0 is
that they match at a later upstream merge, not only at the fork point. The maintainer runs it; see the
script's header for its inputs.

## Record store acceptance

`scripts/plunge-record-store-acceptance.sh` is the maintainer's run of the record backend on a real
store (INTENTIUS/plunge#6). It defaults to floci's S3 on localhost, refuses a store that is not on
loopback unless `PLUNGE_ACCEPTANCE_REAL_STORE=yes`, and writes `VERDICT` lines for two concurrent
updates on disjoint resources, one record per URN in the store listing, and an older program's run
leaving a newer run's resource in place. See the script's header for its inputs and outputs.

## Merging upstream

```sh
git fetch upstream
git merge upstream/master
```

Then check that no new workflow landed in `.github/workflows` (the `plunge-upstream-guard` workflow
fails if one did), and rebuild. The Pulumi-compatible version follows `CHANGELOG.md` on its own.

### Scheduled upstream merge

`scripts/plunge-upstream-merge.sh` does the same on a fresh branch. It fetches pulumi/pulumi master
(fetch only; it never pushes there), cuts `upstream-merge/<date>-<upstream sha>` from
`origin/integration/pulumi-engine` (`BASE` to change it), merges with a merge commit, moves any workflow
upstream added into `.github/upstream-workflows/` in its own commit, and builds `sdk`, `pkg`, `sdk/pcl`,
the three language hosts (with `GOTOOLCHAIN=go1.27.1`) and `bin/plunge`. A conflict only in upstream's
generated Automation API or in go.mod/go.sum files is resolved (it takes upstream's, re-applies
`scripts/plunge-go-mod-fork.txt`, then runs `go mod tidy` in every module `scripts/tidy.sh` covers). On
any other conflict it lists the files, aborts the merge and stops: a human resolves it. With
`--push` it pushes the branch to origin, and only `upstream-merge/*` branches. It never merges into the
integration branch or main. Results go to `plunge-upstream-merge-out/`: `report.md`, and `verdict.txt`
with one line, `UPSTREAM-MERGE UP-TO-DATE|MERGED|CONFLICT|BUILD-FAILED|ERROR ...`.

The `plunge-upstream-merge` workflow runs it every Monday with `--push` and opens an issue with the
report. It opens no PR. Pushing upstream's history needs a token with the `workflows` permission, so set
the `PLUNGE_MERGE_TOKEN` secret; GITHUB_TOKEN can't push commits that touch `.github/workflows`.
Scheduled workflows only run from the default branch, so the schedule starts once the file is on main.

### SDK check

SDKs ask the engine what it supports, either through GetDeploymentInfo's `SupportedFeatures` (current
SDKs) or through `SupportsFeature("<id>")` (older ones, and the fallback). `pkg/cmd/plunge-sdk-check`
takes the latest release of each SDK (nodejs and python from npm and PyPI, go from the Go proxy, dotnet
from NuGet with the source at its pulumi-dotnet tag, java's sources jar from Maven Central, and yaml
through the Go SDK version pulumi-yaml's `go.mod` pins), reads the features its code asks for, and asks
plunge's resource monitor the same questions through its own handlers (`deploy.QueryMonitorFeatures`).
It also compares the SDK's minimum CLI version with plunge's Pulumi-compatible version.

```sh
cd pkg
go run ./cmd/plunge-sdk-check -pulumi-version "$(../scripts/plunge-version.sh pulumi)" -out verdicts.txt
# a local tree instead of a download:
go run ./cmd/plunge-sdk-check -lang nodejs -source nodejs=../sdk/nodejs -source-version nodejs=dev \
  -pulumi-version "$(../scripts/plunge-version.sh pulumi)"
```

Verdict lines:

```
MONITOR-FALSE plunge - <feature> (...)        plunge's monitor answers false
SDK <lang> <version> files=.. legacy=.. enum=.. missing=..
VERSION-OK|VERSION-TOO-OLD|VERSION-UNKNOWN <lang> <version> min-cli=.. plunge-pulumi=..
SDK-AHEAD <lang> <version> plunge-pulumi=..   a nodejs/python/go SDK newer than plunge's upstream base
FEATURE-OK <lang> <version> <feature> (...)
FEATURE-MISSING <lang> <version> <feature> (...)
SDK-ERROR <lang> ...                          the release couldn't be fetched, or no feature checks were found
```

A feature named by an enum value plunge's proto doesn't have yet is `FEATURE-MISSING` with the note
"unknown to plunge's proto; merge upstream". The `plunge-sdk-check` workflow runs the check daily and
keeps one open issue for `FEATURE-MISSING`, `VERSION-TOO-OLD` and `SDK-ERROR` lines.

Upstream's README follows.

---

<p align="center">
    <a href="https://www.pulumi.com/?utm_source=github.com&utm_medium=referral&utm_campaign=pulumi-pulumi-github-repo&utm_content=top+logo" title="Pulumi - Modern Infrastructure as Code - AWS Azure Kubernetes Containers Serverless">
        <picture>
            <source media="(prefers-color-scheme: dark)" srcset="https://www.pulumi.com/images/logo/logo-on-black.svg">
            <img src="https://www.pulumi.com/images/logo/logo-on-white.svg" alt="Pulumi" width="350">
        </picture>
    </a>
</p>

[![Slack](https://www.pulumi.com/images/docs/badges/slack.svg)](https://slack.pulumi.com/)
[![GitHub Discussions](https://img.shields.io/github/discussions/pulumi/pulumi)](https://github.com/pulumi/pulumi/discussions)
[![NPM version](https://badge.fury.io/js/%40pulumi%2Fpulumi.svg)](https://www.npmjs.com/package/@pulumi/pulumi)
[![Python version](https://badge.fury.io/py/pulumi.svg)](https://pypi.org/project/pulumi)
[![NuGet version](https://badge.fury.io/nu/pulumi.svg)](https://www.nuget.org/packages/pulumi)
[![GoDoc](https://pkg.go.dev/badge/github.com/pulumi/pulumi.svg)](https://pkg.go.dev/github.com/pulumi/pulumi)
[![License](https://img.shields.io/github/license/pulumi/pulumi)](LICENSE)

# Infrastructure as Code for Humans and Agents

<a href="https://www.pulumi.com/docs/get-started/">
    <img src="https://www.pulumi.com/images/get-started.svg?" align="right" width="120">
</a>

**Pulumi Infrastructure as Code** is the easiest way to build and deploy infrastructure, of any architecture and on any cloud, using programming languages that you already know and love. Code and ship infrastructure faster with your favorite languages and tools, and embed IaC anywhere with [Automation API](https://www.pulumi.com/docs/iac/automation-api/).

Simply write code in your favorite language and Pulumi automatically provisions and manages your resources on
[AWS](https://www.pulumi.com/docs/integrations/clouds/aws/),
[Azure](https://www.pulumi.com/docs/integrations/clouds/azure/),
[Google Cloud Platform](https://www.pulumi.com/docs/integrations/clouds/gcp/),
[Kubernetes](https://www.pulumi.com/docs/integrations/clouds/kubernetes/), and [300+ providers](https://www.pulumi.com/registry/) using an
[infrastructure-as-code](https://www.pulumi.com/what-is/what-is-infrastructure-as-code/) approach.
Skip the YAML, and use standard language features like loops, functions, classes,
and package management that you already know and love.

For example, create three web servers:

```typescript
import * as aws from "@pulumi/aws";

const ami = aws.ec2.getAmiOutput({
    mostRecent: true,
    owners: ["amazon"],
    filters: [{ name: "name", values: ["al2023-ami-*-x86_64"] }],
});

const sg = new aws.ec2.SecurityGroup("web-sg", {
    ingress: [{ protocol: "tcp", fromPort: 80, toPort: 80, cidrBlocks: ["0.0.0.0/0"] }],
});

for (const i of [0, 1, 2]) {
    new aws.ec2.Instance(`web-${i}`, {
        ami: ami.id,
        instanceType: "t3.micro",
        vpcSecurityGroupIds: [sg.id],
        userData: `#!/bin/bash
            echo "Hello, World!" > index.html
            nohup python3 -m http.server 80 &`,
    });
}
```

Or a simple serverless timer that archives Hacker News every day at 8:30AM:

```typescript
import * as aws from "@pulumi/aws";
import { DynamoDBClient } from "@aws-sdk/client-dynamodb";
import { DynamoDBDocumentClient, PutCommand } from "@aws-sdk/lib-dynamodb";

const snapshots = new aws.dynamodb.Table("snapshots", {
    attributes: [{ name: "id", type: "S" }],
    hashKey: "id",
    billingMode: "PAY_PER_REQUEST",
});

aws.cloudwatch.onSchedule("daily-yc-snapshot", "cron(30 8 * * ? *)", async () => {
    const res = await fetch("https://news.ycombinator.com");
    const content = await res.text();

    const client = DynamoDBDocumentClient.from(new DynamoDBClient({}));
    await client.send(new PutCommand({
        TableName: snapshots.name.get(),
        Item: { id: `${Date.now()}`, content },
    }));
});
```

Many examples are available spanning containers, serverless, and infrastructure in
[pulumi/examples](https://github.com/pulumi/examples).

Pulumi is open source under the [Apache 2.0 license](https://github.com/pulumi/pulumi/blob/master/LICENSE), supports many languages and clouds, and is easy to extend.  This
repo contains the `pulumi` CLI, language SDKs, and core Pulumi engine, and individual libraries are in their own repos.

## Welcome

<img align="right" width="400" src="/quickstart_terminal.png" alt="Pulumi quickstart terminal" />

* **[Get Started with Pulumi](https://www.pulumi.com/docs/get-started/)**: Deploy a simple application in AWS, Azure, Google Cloud, or Kubernetes using Pulumi.

* **[Learn](https://www.pulumi.com/tutorials/)**: Follow Pulumi learning pathways to learn best practices and architectural patterns through authentic examples.

* **[Examples](https://github.com/pulumi/examples)**: Browse several examples across many languages,
  clouds, and scenarios including containers, serverless, and infrastructure.

* **[Docs](https://www.pulumi.com/docs/)**: Learn about Pulumi concepts, follow user-guides, and consult the reference documentation.

* **[Registry](https://www.pulumi.com/registry/)**: Find the Pulumi Package with the resources you need. Install the package directly into your project, browse the API documentation, and start building.

* **[Secrets Management](https://www.pulumi.com/product/secrets-management/)**: Tame secrets sprawl and configuration complexity securely across all your cloud infrastructure and applications with Pulumi ESC.

* **[Community Slack](https://slack.pulumi.com/)**: Join us in Pulumi Community Slack. All conversations and questions are welcome.

* **[GitHub Discussions](https://github.com/pulumi/pulumi/discussions)**: Ask questions or share what you're building with Pulumi.

## <a name="getting-started"></a>Getting Started

[![Watch the video](/youtube_preview_image.png)](https://www.youtube.com/watch?v=Q8tw6YTD3ac)

See the [Get Started](https://www.pulumi.com/docs/get-started/) guide to quickly get started with
Pulumi on your platform and cloud of choice.

Otherwise, the following steps demonstrate how to deploy your first Pulumi program, using AWS
Serverless Lambdas, in minutes:

1. **Install**:

    To install the latest Pulumi release, run the following (see full
    [installation instructions](https://www.pulumi.com/docs/install/) for additional installation options):

    ```bash
    curl -fsSL https://get.pulumi.com/ | sh
    ```

2. **Create a Project**:

    After installing, you can get started with the `pulumi new` command:

    ```bash
    mkdir pulumi-demo && cd pulumi-demo
    pulumi new serverless-aws-typescript
    ```

    The `new` command offers [templates](https://github.com/pulumi/templates/) for all languages and clouds.  Run it without an argument and it'll prompt
    you with available projects.  This command creates an AWS Serverless Lambda project written in TypeScript.

3. **Deploy to the Cloud**:

    Run `pulumi up` to get your code to the cloud:

    ```bash
    pulumi up
    ```

    This makes all cloud resources needed to run your code.  Simply make edits to your project, and subsequent
    `pulumi up`s will compute the minimal diff to deploy your changes.

4. **Use Your Program**:

    Now that your code is deployed, you can interact with it.  In the above example, we can curl the endpoint:

    ```bash
    curl $(pulumi stack output url)
    ```

5. **Access the Logs**:

    If you're using containers or functions, Pulumi's unified logging command will show all of your logs:

    ```bash
    pulumi logs -f
    ```

6. **Destroy your Resources**:

    After you're done, you can remove all resources created by your program:

    ```bash
    pulumi destroy -y
    ```

To learn more, head over to [pulumi.com](https://www.pulumi.com/) for much more information, including
[tutorials](https://www.pulumi.com/tutorials/), [examples](https://github.com/pulumi/examples), and
details of the core Pulumi CLI and [programming model concepts](https://www.pulumi.com/docs/iac/concepts/).

## <a name="platform"></a>Platform

### Languages

|    | Language | Status | Runtime | Versions |
| -- | -------- | ------ | ------- | -------- |
| <img src="https://www.pulumi.com/logos/tech/logo-js.png" height=38 />     | [JavaScript](https://www.pulumi.com/docs/iac/languages-sdks/javascript/) | Stable  | Node.js | [Current, Active and Maintenance LTS versions](https://nodejs.org/en/about/previous-releases)  |
| <img src="https://www.pulumi.com/logos/tech/logo-ts.png" height=38 />     | [TypeScript](https://www.pulumi.com/docs/iac/languages-sdks/javascript/) | Stable  | Node.js | [Current, Active and Maintenance LTS versions](https://nodejs.org/en/about/previous-releases)  |
| <img src="https://www.pulumi.com/logos/tech/logo-python.svg" height=38 /> | [Python](https://www.pulumi.com/docs/iac/languages-sdks/python/)     | Stable  | Python | [Supported versions](https://devguide.python.org/versions/#versions) |
| <img src="https://www.pulumi.com/logos/tech/logo-golang.png" height=38 /> | [Go](https://www.pulumi.com/docs/iac/languages-sdks/go/)             | Stable  | Go | [Supported versions](https://go.dev/doc/devel/release#policy) |
| <img src="https://www.pulumi.com/logos/tech/dotnet.svg" height=38 />      | [.NET (C#/F#/VB.NET)](https://www.pulumi.com/docs/iac/languages-sdks/dotnet/)     | Stable  | .NET | [Supported versions](https://dotnet.microsoft.com/en-us/platform/support/policy/dotnet-core#lifecycle)  |
| <img src="https://www.pulumi.com/logos/tech/java.svg" height=38 />      | [Java](https://www.pulumi.com/docs/iac/languages-sdks/java/)     | Stable  | JDK | 11+  |
| <img src="https://www.pulumi.com/logos/tech/yaml.svg" height=38 />      | [YAML](https://www.pulumi.com/docs/iac/languages-sdks/yaml/)     | Stable  | n/a  | n/a  |

### Clouds

Visit the [Registry](https://www.pulumi.com/registry/) for the full list of supported cloud and infrastructure providers.

## Contributing

Visit [CONTRIBUTING.md](https://github.com/pulumi/pulumi/blob/master/CONTRIBUTING.md) for information on building Pulumi from source or contributing improvements.
