#!/usr/bin/env bash
#
# Copies one race of a scripts/plunge-races/races.sh run into the committed recording under
# tests/plunge-races/<race>/. Recorded files are only ever written by this script, never by hand, as with
# scripts/plunge-baseline-record.sh.
#
# Usage:
#   scripts/plunge-races/record.sh <OUT of a races.sh run> <race>
#
# The run must have gone through scripts/plunge-safe-run.sh with `--log <OUT>/safe-run.log --scan <OUT>`, so
# <OUT>/safe-run-verdict.txt says CLEAN. A run without that file, with another verdict, with no VERDICT line for
# <race>, or with any FAIL for it, is refused and nothing is written.
#
# It replaces tests/plunge-races/<race>/ with <OUT>/<race>/ (logs, records listings, export, drift NDJSON, the
# checkouts' Pulumi.yaml and stack files; dotfiles left out), plus the race's VERDICT lines as verdicts.txt,
# versions.txt, safe-run-verdict.txt and safe-run.log.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

die() {
  >&2 echo "plunge-races-record: $*"
  exit 2
}

[ $# -eq 2 ] || die "usage: scripts/plunge-races/record.sh <OUT of a races.sh run> <race>"
OUT="$1"
RACE="$2"
case "${RACE}" in
  ''|*[!a-z0-9-]*) die "${RACE} is not a race name" ;;
esac
[ -d "${OUT}/${RACE}" ] || die "${OUT}/${RACE} is not a directory: did the run include ${RACE}?"
for f in verdicts.txt versions.txt safe-run.log; do
  [ -s "${OUT}/${f}" ] || die "${OUT}/${f} is missing or empty"
done
safe="$(cat "${OUT}/safe-run-verdict.txt" 2>/dev/null || true)"
[ "${safe}" = CLEAN ] ||
  die "${OUT}/safe-run-verdict.txt says '${safe:-(missing)}', not CLEAN. Re-run under scripts/plunge-safe-run.sh --log ${OUT}/safe-run.log --scan ${OUT}"

lines="$(awk -v r="${RACE}" '$1 == "VERDICT" && ($3 == r || index($3, r "-") == 1)' "${OUT}/verdicts.txt")"
[ -n "${lines}" ] || die "no VERDICT line for ${RACE} in ${OUT}/verdicts.txt"
if printf '%s\n' "${lines}" | awk '$4 != "PASS"' | grep -q .; then
  die "${RACE} has a verdict other than PASS in ${OUT}/verdicts.txt; only passing runs are recorded"
fi

DEST="${ROOT}/tests/plunge-races/${RACE}"
mkdir -p "${DEST}"
# Clear the previous recording's files (the directory is this script's alone), then copy the run.
find "${DEST}" -type f -delete
(cd "${OUT}/${RACE}" && find . -type f ! -name '.*' -print) | while read -r f; do
  mkdir -p "${DEST}/$(dirname "${f}")"
  cp "${OUT}/${RACE}/${f}" "${DEST}/${f}"
done
printf '%s\n' "${lines}" > "${DEST}/verdicts.txt"
cp "${OUT}/versions.txt" "${OUT}/safe-run-verdict.txt" "${OUT}/safe-run.log" "${DEST}/"
echo "recorded ${RACE} into ${DEST}"
echo "Review with: git -C ${ROOT} status -- tests/plunge-races"
