#!/usr/bin/env bash
#
# scripts/check-emulator-images.sh <choudoufu checkout> [ref]
#
# plunge's corpus and conformance runs use the emulator images pinned in tests/plunge-corpus/emulator-images.
# Those are copies of choudoufu's pins, one file per image there:
#
#   floci      live/floci-image
#   floci-gcp  live/floci-gcp-image
#   floci-az   live/floci-az-image
#
# This diffs each copy against choudoufu's, so a pin moved on one side is caught before a run measures two
# different emulators. With ref, choudoufu's files are read from that git ref of the checkout (git show)
# rather than from its working tree, e.g.
#   scripts/check-emulator-images.sh ../choudoufu origin/integration/consolidation-graph
#
# The last line is the verdict: "emulator-images: SAME (3 images)" with exit status 0, or
# "emulator-images: DIFFERENT" / "emulator-images: ERROR ..." with a nonzero status. Read the verdict line,
# not only the status.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OURS="${ROOT}/tests/plunge-corpus/emulator-images"

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
  echo "usage: scripts/check-emulator-images.sh <choudoufu checkout> [ref]" >&2
  echo "emulator-images: ERROR usage"
  exit 2
fi
chdf="$1"
ref="${2:-}"

if [ ! -f "${OURS}" ]; then
  echo "emulator-images: ERROR plunge's copy ${OURS} is missing"
  exit 2
fi

# theirs <relative path> prints choudoufu's pin, from the ref or the working tree.
theirs() {
  if [ -n "${ref}" ]; then
    git -C "${chdf}" show "${ref}:$1" 2>/dev/null
  else
    cat "${chdf}/$1" 2>/dev/null
  fi
}

# ours <name> prints the image this repo pins for name.
ours() {
  awk -v n="$1" '$1 == n { print $2 }' "${OURS}"
}

different=0
count=0
for pair in floci:live/floci-image floci-gcp:live/floci-gcp-image floci-az:live/floci-az-image; do
  name="${pair%%:*}"
  rel="${pair#*:}"
  mine="$(ours "${name}")"
  if [ -z "${mine}" ]; then
    echo "emulator-images: ERROR ${OURS} has no line for ${name}"
    exit 2
  fi
  if ! raw="$(theirs "${rel}")"; then
    echo "emulator-images: ERROR cannot read ${rel}${ref:+ at ${ref}} in ${chdf}"
    exit 2
  fi
  # choudoufu's pin files hold the image on one line; ignore blank lines and surrounding space.
  other="$(printf '%s\n' "${raw}" | grep -v '^[[:space:]]*$' | head -1 | tr -d '[:space:]')"
  count=$((count + 1))
  if [ "${mine}" = "${other}" ]; then
    echo "same      ${name}  ${mine}"
  else
    echo "different ${name}"
    echo "  plunge     tests/plunge-corpus/emulator-images: ${mine}"
    echo "  choudoufu  ${chdf}${ref:+@${ref}}:${rel}: ${other:-(empty)}"
    different=1
  fi
done

if [ "${different}" -ne 0 ]; then
  echo "emulator-images: DIFFERENT"
  exit 1
fi
echo "emulator-images: SAME (${count} images)"
