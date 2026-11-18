// plunge's release, run by `chant release` (INTENTIUS/plunge#70). Regenerate the workflow with
//
//   npx --yes @intentius/chant@0.124.104 release workflow
//
// Dispatch .github/workflows/plunge-chant-release.yml with the label (`0.3.0`, or `auto`). The cut pushes
// the tag plunge-v<label> and the publish job makes the GitHub release.
//
// goreleaser builds the archives in snapshot mode with .goreleaser.plunge.yml and chant uploads what it
// writes; upstream's .goreleaser.yml is not read. The workflow file keeps the plunge- prefix because
// plunge-upstream-guard fails on any other workflow in .github/workflows.
export default {
  release: {
    tag: "plunge-v{version}",
    // Tags cut before the ledger existed (plunge-v0.1.0, plunge-v0.2.0) count as earlier releases.
    legacyTags: ["plunge-v"],
    // plunge has no pull request CI. plunge-upstream-guard runs on every push to main, so the commit
    // has a check to pass, and a failing scheduled run (plunge-sdk-check) on it refuses the cut too.
    green: "forge-checks",
    workflow: {
      trigger: "dispatch",
      gate: "head",
      file: ".github/workflows/plunge-chant-release.yml",
      chant: "npx --yes @intentius/chant@0.124.104",
      // The doctor has known false failures in the cut job until the next chant release.
      doctor: false,
      env: {
        // scripts/prep-for-goreleaser.sh downloads the dotnet and yaml hosts with gh, and the release target reads it.
        GITHUB_TOKEN: "${{ github.token }}",
      },
      setup: [
        { uses: "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0", with: { "go-version-file": "pkg/go.mod", "cache-dependency-path": "pkg/go.sum\nsdk/go.sum\n" } },
        // The Pulumi-compatible version is the top heading of CHANGELOG.md; .goreleaser.plunge.yml stamps it.
        { run: 'echo "PULUMI_VERSION=$(./scripts/plunge-version.sh pulumi)" >> "$GITHUB_ENV"' },
        // Fills bin/<os>-<arch>/ with the hosts the archives bundle.
        { run: "./scripts/prep-for-goreleaser.sh" },
      ],
    },
    artifacts: [
      // The config's snapshot version is the label. goreleaser's dist is goreleaser/ in that file.
      { name: "goreleaser", goreleaser: { config: ".goreleaser.plunge.yml", dist: "goreleaser" } },
    ],
    publish: [
      {
        type: "forge",
        forge: "github",
        repo: "INTENTIUS/plunge",
        assets: ["artifact:goreleaser"],
        sbom: false,
        notes: { generate: "forge" },
      },
    ],
  },
};
