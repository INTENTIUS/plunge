# plunge

Pulumi with per-resource locking.

The engine plywood can run with `engine: plunge`.

plunge runs stock Pulumi's programs, SDKs,
language hosts and providers, and changes where state lives and how a run decides ownership:

- Each taggable resource carries two markers, `plunge-stack` and `plunge-urn`, written in its create
  call (tags on AWS and Azure, digests in labels on Google Cloud and Kubernetes).
- A `plunge+<store>` backend (`file`, `s3`, `gs`, `azblob`, `k8s`) keeps one record per resource, each
  write conditional, with no stack lock.
- An unnamed resource of a covered type gets the same name on every run, and a saved plan whose
  records moved is refused (exit 40). Each delete is checked against the live marker
  (INTENTIUS/plunge#37).
- `plunge stack adopt` moves a stock stack onto records; `plunge drift --resource` reads one resource's
  drift with one provider Read.

The ownership model is chant's identity markers, carried into Pulumi as choudoufu carries them into
OpenTofu. Both are chant engines (a chant workspace runs a plunge stack as a member) that also work
standalone, planned in INTENTIUS/plunge#2 with the design record in INTENTIUS/plunge#9.

The documentation at https://intentius.io/plunge is built from `site/`. Design details are in
`docs/plunge/CONCURRENCY.md` and `docs/plunge/ADOPT.md`. `just site-serve` (live reload) and
`just site-preview` (the built site) serve http://127.0.0.1:4321/plunge/, or another port given as an
argument; `just site` builds it with its link and contrast checks.

## Differences from stock Pulumi

- The backend URL gains a `plunge+` prefix, and each taggable resource gets `plunge-stack` and
  `plunge-urn` tags.
- A new unnamed resource gets its name plus seven hex digits, the same on every run.
- A delete, `plunge destroy` included, is refused when a taggable object has lost its markers.
- `plunge preview` and `plunge up` refresh from the cloud first. `PLUNGE_REFRESH=false` turns that off.
- No stack history or checkpoint backups. Bucket versioning keeps earlier versions of each record.
- Replacing a resource whose name plunge chose deletes the old object before creating the new one
  ([#12](https://github.com/INTENTIUS/plunge/issues/12)).

## Install

plunge `v0.2.0` (Pulumi-compatible `3.272.0`) is on the
[releases page](https://github.com/INTENTIUS/plunge/releases/tag/plunge-v0.2.0) with a checksums
file, for darwin, linux and windows on arm64 and x64. Each archive unpacks to `plunge/` (on Windows
`plunge/bin/`), the CLI beside its language hosts; put that directory on `PATH`. The
[quickstart](https://intentius.io/plunge/use/start/#quickstart) has the commands and the checksum
check, and runs a first preview offline in five minutes; [Install](https://intentius.io/plunge/use/start/#install)
covers the macOS quarantine step, a permanent `PATH` and uninstalling.

## Fork point

Forked from [pulumi/pulumi](https://github.com/pulumi/pulumi) master at `7e12743da8` (2026-10-25,
after the 3.272.0 release), with upstream history kept. The `upstream` remote points at pulumi/pulumi,
merged in, never rebased.

Choices that keep those merges cheap:

- Go module paths stay `github.com/pulumi/pulumi/...` and the CLI's main package stays at
  `pkg/cmd/pulumi`; only the output name changes, to `plunge`.
- plunge-only code goes in new files where it can: `pkg/plunge/`, `build/plunge.mk`,
  `.goreleaser.plunge.yml`, `scripts/plunge-*.sh`. Upstream's `.goreleaser.yml` and Makefile targets
  stay.
- Plugin protocol names are unchanged: SDKs and providers look up `pulumi-language-<lang>` hosts and
  `pulumi-resource-<name>` providers.
- Upstream's GitHub workflows sit, inactive, in `.github/upstream-workflows/` (see the README there).

## Versions

A plunge build carries two versions:

- plunge's own, from `.plunge-version` (or `PLUNGE_VERSION`), and
- the Pulumi-compatible version, the newest upstream release merged in (the top heading of
  `CHANGELOG.md`, or `PULUMI_VERSION`). The engine reports it wherever upstream reports "the CLI
  version": a project's `requiredPulumiVersion`, a plugin's `pulumiVersionRange`, and the
  `RequirePulumiVersion` RPC that SDKs call (`pulumi.requirePulumiVersion` and friends).

`scripts/plunge-version.sh plunge|pulumi|ldflags` prints them.

```console
$ plunge version
plunge v0.2.0
pulumi v3.272.0
$ plunge version --pulumi-compatible
v3.272.0
```

Through a symlink named `pulumi`, `version` prints only the Pulumi-compatible version, the bare semver
Automation API parses.

plunge doesn't run upstream's update check, which would point at pulumi's releases and installers.

The generated Automation API wrappers (`sdk/go/auto/automation`, `sdk/nodejs/automation/interface`,
`sdk/python/lib/pulumi/automation/interface`) are upstream's, regenerated on each upstream merge.
plunge's `drift` and `stack adopt` are hand-written beside them: Go's `API.Drift` and
`API.StackAdopt` in `sdk/go/auto/automation/plunge_commands.go` (options in `optplungedrift` and
`optplungestackadopt`), and a `PlungeAPI` class (the generated API plus `drift` and `stackAdopt` /
`stack_adopt`) in `sdk/nodejs/automation/plunge.ts` and `sdk/python/lib/pulumi/automation/plunge.py`.

## Building

Tools come from `.mise.toml`, as upstream (see `CLAUDE.md`).

```sh
make plunge                  # bin/plunge, both versions stamped
make build                   # upstream's build (CLI, SDKs, language hosts) plus bin/plunge
make plunge_language_hosts   # fetch the bundled dotnet and yaml hosts into bin/ for this machine
mise run plunge-build        # bin/plunge without make
```

`make plunge` needs mise's protoc. On a fresh checkout its `.make/proto` step runs
`proto/generate.sh`, which also needs mise's protoc-gen-go and protoc-gen-go-grpc and the node protoc
plugins. The generated code is committed, so building without mise needs no protoc:

```sh
go build -C pkg -o ../bin/plunge -ldflags "$(./scripts/plunge-version.sh ldflags)" ./cmd/pulumi
```

Without `-ldflags` both versions are empty and the version checks are off, as on an upstream dev build.

The language hosts are separate Go modules and build with a plain `go build`, for example
`go build -C sdk/nodejs/cmd/pulumi-language-nodejs -o ../../../../bin/pulumi-language-nodejs .`.
Build to `bin/` (ignored) or `-o /dev/null`; `.gitignore` also covers the binary a bare `go build`
leaves in the source directory.

## Language hosts

The release archive follows upstream's layout: next to the `plunge` binary, where the CLI looks, sit
the in-repo hosts (nodejs, bun, python, go, pcl) built from this tree and the dotnet and yaml hosts from
their pulumi releases at the versions in `scripts/get-language-providers.sh`. The CLI installs Java and
HCL on first use, at the versions in `pkg/util/plugin.go` (`knownLanguageRuntimes`).

A host neither bundled nor on `PATH` is fetched as upstream fetches it: stock pulumi's build at
plunge's Pulumi-compatible version, the same code while plunge's hosts match upstream's.

Pushing a `plunge-vX.Y.Z` tag runs `.github/workflows/plunge-release.yml`; a bare `vX.Y.Z` tag, upstream
pulumi's naming, doesn't (INTENTIUS/plunge#33). goreleaser's release mode refuses the `plunge-v` prefix,
so the workflow runs goreleaser with `.goreleaser.plunge.yml` in snapshot mode and publishes the
`plunge-vX.Y.Z-<os>-<arch>` archives and checksums with `gh release create` under the pushed tag, with
the Windows binaries unsigned. The first was `plunge-v0.1.0`, on 2026-10-27. The snapshot build below
lists the same artifacts and publishes nothing.

```sh
./scripts/prep-for-goreleaser.sh
PULUMI_VERSION=$(./scripts/plunge-version.sh pulumi) PLUNGE_VERSION=$(./scripts/plunge-version.sh plunge) \
  goreleaser release -f .goreleaser.plunge.yml --snapshot --clean
```

## Baseline against stock Pulumi

`scripts/plunge-baseline.sh` runs `plunge preview` and stock `pulumi preview` on each program in
`tests/plunge-corpus/` against the local emulators and compares them. P-L0 accepts a match at a later
upstream merge as well as at the fork point. The maintainer runs it with the inputs its header documents.

## Record store acceptance

The record store acceptance check is `scripts/plunge-races/races.sh lifecycle` on floci's S3, with a
`plunge+s3://` store (INTENTIUS/plunge#6). Its `VERDICT` lines cover preview, up, two concurrent updates
on disjoint resources with no lock object in the store, an export that assembles with integrity checking
and has one record per URN, an older program's run keeping a newer run's resource, drift and destroy. The
recorded verdicts are in [`tests/plunge-races/lifecycle/verdicts.txt`](tests/plunge-races/lifecycle/verdicts.txt).
`races.sh readback` runs the same concurrent check on pulumi-random RandomStrings, whose refresh reads
inputs back in the provider's form, and checks that a plain re-run writes no record. The environment is
in `scripts/plunge-races/lib.sh`.

## Merging upstream

```sh
git fetch upstream
git merge upstream/master
```

Then check that no new workflow landed in `.github/workflows` (the `plunge-upstream-guard` workflow
fails if one did), and rebuild. The Pulumi-compatible version follows `CHANGELOG.md` by itself.

### Scheduled upstream merge

`scripts/plunge-upstream-merge.sh` does the same on a fresh branch. It fetches pulumi/pulumi master,
setting the `upstream` push URL to `no-push://`, cuts `upstream-merge/<date>-<upstream sha>` from
`origin/integration/pulumi-engine` (or `BASE`), merges with a merge commit, moves any new upstream
workflow into `.github/upstream-workflows/` in its own commit, and builds `sdk`, `pkg`, `sdk/pcl`, the
three language hosts (with `GOTOOLCHAIN=go1.27.1`) and `bin/plunge`. Conflicts confined to upstream's
generated Automation API or go.mod/go.sum files take upstream's side, then it re-applies
`scripts/plunge-go-mod-fork.txt` and runs `go mod tidy` in each module `scripts/tidy.sh` covers. Any
other conflict aborts the merge with the file list, for a human. `--push` pushes only
`upstream-merge/*` branches to origin; it never merges into the integration branch or main. Results go
to `plunge-upstream-merge-out/`: `report.md`, and `verdict.txt` with one line,
`UPSTREAM-MERGE UP-TO-DATE|MERGED|CONFLICT|BUILD-FAILED|ERROR ...`.

The `plunge-upstream-merge` workflow runs it every Monday with `--push` and opens an issue (no PR) with
the report. GITHUB_TOKEN can't push commits touching `.github/workflows`, so set `PLUNGE_MERGE_TOKEN`
to a token with the `workflows` permission. The schedule starts once the file is on main, the default
branch.

### SDK check

SDKs query engine features through GetDeploymentInfo's `SupportedFeatures` (current SDKs) or
`SupportsFeature("<id>")` (older ones, and the fallback). `pkg/cmd/plunge-sdk-check` takes each SDK's
latest release (nodejs and python from npm and PyPI, go from the Go proxy, dotnet from NuGet with the
source at its pulumi-dotnet tag, java's sources jar from Maven Central, yaml through the Go SDK version
pulumi-yaml's `go.mod` pins), puts the features its code asks for to plunge's resource monitor
(`deploy.QueryMonitorFeatures`), and compares its minimum CLI version with plunge's Pulumi-compatible
version.

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

An enum value missing from plunge's proto gives `FEATURE-MISSING` with the note "unknown to plunge's
proto; merge upstream". The daily `plunge-sdk-check` workflow keeps one open issue for
`FEATURE-MISSING`, `VERSION-TOO-OLD` and `SDK-ERROR` lines.

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
