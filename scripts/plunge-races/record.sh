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
# <OUT>/safe-run-verdict.txt says CLEAN.

# With PLUNGE_RACES_TARGET=aws the run was against real AWS (lib.sh), so plunge-safe-run.sh was not used: it
# would scrub the credentials. Instead the files are scanned for the caller's own credential values and for key id
# shapes (AKIA, ASIA), the recording goes to tests/plunge-races-aws/<race>/ with a credential-scan.txt in place of
# safe-run.log, and a failing race is recorded too, with its FAIL verdict lines, because a failure on real AWS is
# a result. A run without that file, with another verdict, with no VERDICT line for
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

[ $# -eq 2 ] || [ $# -eq 3 ] || die "usage: scripts/plunge-races/record.sh <OUT of a races.sh run> <race> [directory-name]"
OUT="$1"
RACE="$2"
# The optional directory name keeps an earlier attempt next to the final one (aws mode: c2-attempt1).
LABEL="${3:-$2}"
TARGET="${PLUNGE_RACES_TARGET:-floci}"
case "${RACE}" in
  ''|*[!a-z0-9-]*) die "${RACE} is not a race name" ;;
esac
[ -d "${OUT}/${RACE}" ] || die "${OUT}/${RACE} is not a directory: did the run include ${RACE}?"
need="verdicts.txt versions.txt safe-run.log"
[ "${TARGET}" = aws ] && need="verdicts.txt versions.txt"
for f in ${need}; do
  [ -s "${OUT}/${f}" ] || die "${OUT}/${f} is missing or empty"
done
if [ "${TARGET}" = aws ]; then
  safe=CLEAN
else
  safe="$(cat "${OUT}/safe-run-verdict.txt" 2>/dev/null || true)"
fi
[ "${safe}" = CLEAN ] ||
  die "${OUT}/safe-run-verdict.txt says '${safe:-(missing)}', not CLEAN. Re-run under scripts/plunge-safe-run.sh --log ${OUT}/safe-run.log --scan ${OUT}"

if [ "${TARGET}" = aws ]; then
  # Exact claim names only: c1 must not pick up c1-native's lines.
  lines="$(awk -v r="${RACE}" '$1 == "VERDICT" && ($3 == r || $3 == r "-control" || $3 == r "-no-locks" || (r == "lifecycle" && index($3, "lifecycle-") == 1))' "${OUT}/verdicts.txt")"
else
  lines="$(awk -v r="${RACE}" '$1 == "VERDICT" && ($3 == r || index($3, r "-") == 1)' "${OUT}/verdicts.txt")"
fi
[ -n "${lines}" ] || die "no VERDICT line for ${RACE} in ${OUT}/verdicts.txt"
if [ "${TARGET}" != aws ] && printf '%s\n' "${lines}" | awk '$4 != "PASS"' | grep -q .; then
  die "${RACE} has a verdict other than PASS in ${OUT}/verdicts.txt; only passing runs are recorded"
fi

DEST="${ROOT}/tests/plunge-races/${LABEL}"
[ "${TARGET}" = aws ] && DEST="${ROOT}/tests/plunge-races-aws/${LABEL}"
mkdir -p "${DEST}"
# Clear the previous recording's files (the directory is this script's alone), then copy the run.
find "${DEST}" -type f -delete
(cd "${OUT}/${RACE}" && find . -type f ! -name '.*' -print) | while read -r f; do
  mkdir -p "${DEST}/$(dirname "${f}")"
  cp "${OUT}/${RACE}/${f}" "${DEST}/${f}"
done
printf '%s\n' "${lines}" > "${DEST}/verdicts.txt"
if [ "${TARGET}" = aws ]; then
  cp "${OUT}/versions.txt" "${DEST}/"
  # The end-of-run listing of the account's plr* leftovers (races.sh writes it), kept with the race.
  [ -f "${OUT}/leftovers.txt" ] && cp "${OUT}/leftovers.txt" "${DEST}/"
  creds="$(aws configure export-credentials --format env 2>/dev/null | sed -n 's/^export AWS_[A-Z_]*=//p' | sed '/^$/d' || true)"
  hits=0
  if [ -n "${creds}" ]; then
    printf '%s\n' "${creds}" > "${TMPDIR:-/tmp}/.plr-creds.$$"
    grep -rlF -f "${TMPDIR:-/tmp}/.plr-creds.$$" "${DEST}" > "${DEST}/.hits" 2>/dev/null && hits=1
    rm -f "${TMPDIR:-/tmp}/.plr-creds.$$"
  fi
  if grep -rlE '(AKIA|ASIA)[A-Z0-9]{16}|aws_secret_access_key|AWS_SECRET_ACCESS_KEY=|AWS_SESSION_TOKEN=' "${DEST}" >> "${DEST}/.hits" 2>/dev/null; then hits=1; fi
  if [ "${hits}" = 1 ]; then
    cat "${DEST}/.hits" >&2
    rm -rf "${DEST}"
    die "credential-like content in the ${RACE} recording; nothing written"
  fi
  rm -f "${DEST}/.hits"
  echo "credential scan: no AWS credential value, key id or secret key name in any file under ${DEST##*/}" > "${DEST}/credential-scan.txt"
else
  cp "${OUT}/versions.txt" "${OUT}/safe-run-verdict.txt" "${OUT}/safe-run.log" "${DEST}/"
fi
echo "recorded ${RACE} as ${LABEL} into ${DEST}"
echo "Review with: git -C ${ROOT} status -- tests/plunge-races tests/plunge-races-aws"
