#!/usr/bin/env bash
#
# Copies one scripts/plunge-baseline.sh run into the corpus's committed baseline (tests/plunge-corpus/README.md,
# "Re-recording the baseline"). The baseline files are only ever written by this script, never by hand.
#
# Usage:
#   scripts/plunge-baseline-record.sh <OUT of a plunge-baseline.sh run>
#
# The run must have gone through scripts/plunge-safe-run.sh with `--scan <OUT>`, so <OUT>/safe-run-verdict.txt
# says CLEAN: no output of the run shows a public cloud rejecting a call. A run without that file, or with
# another verdict, is refused.
#
# For each program in <OUT>/verdicts.txt it writes, under tests/plunge-corpus/<program>/baseline/:
#   preview.json         the run's stock pulumi.preview.json
#   plunge-compare.txt   the run's compare.txt
#   pulumi-version.txt   stock pulumi's version, from the run's versions.txt
# and replaces that program's line in tests/plunge-corpus/baseline-verdicts.txt with the run's, keeping the other
# programs' lines and their order (a new program's line goes at the end). A program whose verdict is ERROR or
# DIFF-STEPS is refused, and nothing is written for any program.
#
# Environment:
#   CORPUS   corpus directory (default: tests/plunge-corpus)

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORPUS="${CORPUS:-${ROOT}/tests/plunge-corpus}"

die() {
  >&2 echo "plunge-baseline-record: $*"
  exit 2
}

[ $# -eq 1 ] || die "usage: scripts/plunge-baseline-record.sh <OUT of a plunge-baseline.sh run>"
OUT="$1"
[ -d "${OUT}" ] || die "${OUT} is not a directory"
[ -f "${OUT}/verdicts.txt" ] || die "${OUT}/verdicts.txt is missing: is ${OUT} a plunge-baseline.sh output?"
[ -f "${OUT}/versions.txt" ] || die "${OUT}/versions.txt is missing"

safe="$(cat "${OUT}/safe-run-verdict.txt" 2>/dev/null || true)"
if [ "${safe}" != "CLEAN" ]; then
  die "${OUT}/safe-run-verdict.txt says '${safe:-(missing)}', not CLEAN. Re-run under scripts/plunge-safe-run.sh --scan ${OUT} -- scripts/plunge-baseline.sh"
fi

# Stock pulumi's version: the indented line under "pulumi: <path>" in versions.txt.
version="$(awk '/^pulumi: / { getline; gsub(/^[[:space:]]+|[[:space:]]+$/, ""); print; exit }' "${OUT}/versions.txt")"
[ -n "${version}" ] || die "no stock pulumi version in ${OUT}/versions.txt"

programs=()
while read -r tag prog v; do
  [ "${tag}" = VERDICT ] || continue
  case "${v}" in
    MATCH|DIFF-INPUTS) ;;
    *) die "${prog} is ${v} in ${OUT}/verdicts.txt; only MATCH and DIFF-INPUTS are recorded (read ${OUT}/${prog}/compare.txt)" ;;
  esac
  [ -d "${CORPUS}/${prog}" ] || die "${prog} is not a program under ${CORPUS}"
  for f in pulumi.preview.json compare.txt; do
    [ -s "${OUT}/${prog}/${f}" ] || die "${OUT}/${prog}/${f} is missing or empty"
  done
  programs+=("${prog}")
done < "${OUT}/verdicts.txt"
[ "${#programs[@]}" -gt 0 ] || die "no VERDICT lines in ${OUT}/verdicts.txt"

for prog in "${programs[@]}"; do
  b="${CORPUS}/${prog}/baseline"
  mkdir -p "${b}"
  cp "${OUT}/${prog}/pulumi.preview.json" "${b}/preview.json"
  cp "${OUT}/${prog}/compare.txt" "${b}/plunge-compare.txt"
  printf '%s\n' "${version}" > "${b}/pulumi-version.txt"
  echo "recorded ${prog}"
done

# Merge the verdict lines: the run's line replaces a program's existing one in place.
committed="${CORPUS}/baseline-verdicts.txt"
[ -f "${committed}" ] || : > "${committed}"
tmp="$(mktemp "${committed}.XXXXXX")"
awk '
  FNR == NR { if ($1 == "VERDICT") { run[$2] = $0; order[++n] = $2 } next }
  $1 == "VERDICT" && ($2 in run) { print run[$2]; done[$2] = 1; next }
  { print }
  END { for (i = 1; i <= n; i++) if (!(order[i] in done)) print run[order[i]] }
' "${OUT}/verdicts.txt" "${committed}" > "${tmp}"
mv "${tmp}" "${committed}"
echo "updated ${committed}"
echo "Review with: git -C ${ROOT} diff --stat -- tests/plunge-corpus"
