#!/usr/bin/env bash
#
# Prints one of plunge's two versions.
#
#   scripts/plunge-version.sh plunge   plunge's own version
#   scripts/plunge-version.sh pulumi   the Pulumi-compatible version
#   scripts/plunge-version.sh ldflags  the -X flags that stamp both into the binary
#
# plunge's version comes from PLUNGE_VERSION, else .plunge-version with "-dev.0" appended.
#
# The Pulumi-compatible version is the newest upstream pulumi/pulumi release merged into this tree. It
# comes from PULUMI_VERSION, else the top "## X.Y.Z" heading of CHANGELOG.md, which upstream writes on
# every release, so it moves forward by itself at each upstream merge. SDKs, language hosts and plugins
# check this version (requiredPulumiVersion, pulumiVersionRange, the RequirePulumiVersion RPC).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

plunge_version() {
  if [ -n "${PLUNGE_VERSION:-}" ]; then
    echo -n "${PLUNGE_VERSION#v}"
  else
    echo -n "$(tr -d '[:space:]' < "${ROOT}/.plunge-version")-dev.0"
  fi
}

pulumi_version() {
  if [ -n "${PULUMI_VERSION:-}" ]; then
    echo -n "${PULUMI_VERSION#v}"
    return
  fi
  local v
  v="$(sed -n 's/^## \([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' "${ROOT}/CHANGELOG.md" | head -n 1)"
  if [ -z "${v}" ]; then
    >&2 echo "plunge-version: no release heading in CHANGELOG.md; set PULUMI_VERSION"
    exit 1
  fi
  echo -n "${v}"
}

case "${1:-plunge}" in
  plunge) plunge_version ;;
  pulumi) pulumi_version ;;
  ldflags)
    echo -n "-X github.com/pulumi/pulumi/sdk/v3/go/common/version.Version=v$(pulumi_version)"
    echo -n " -X github.com/pulumi/pulumi/pkg/v3/plunge.Version=v$(plunge_version)"
    ;;
  *)
    >&2 echo "usage: $0 plunge|pulumi|ldflags"
    exit 2
    ;;
esac
