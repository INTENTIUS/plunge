#!/usr/bin/env bash
#
# Adoption and back-out on floci (INTENTIUS/plunge#7's two maintainer-run acceptance items). The maintainer runs
# this by hand against a local floci; nothing here talks to AWS or Pulumi Cloud.
#
# Usage:
#   scripts/plunge-races/adopt-and-back-out.sh [program]
#
# program is a corpus directory (default: aws-yaml). Its stack is `floci` (Pulumi.floci.yaml).
#
# Steps, each with a verdict line in $OUT/verdicts.txt:
#   1. stock `pulumi up` creates the program on a stock file backend.
#   2. ADOPT-GATED: `plunge preview` against that backend (plunge+file://) is refused with AdoptionRequiredError.
#   3. ADOPT-RECORDED: `plunge stack adopt --yes` writes stacks/<project>/floci/adoption.
#   4. ADOPT-NOOP: a second `plunge stack adopt --yes` writes nothing (the record tree is byte-identical).
#   5. ADOPT-MARKS: the next `plunge preview` plans only in-place updates of tags (and the bridge's tagsAll) adding
#      plunge-* keys; then `plunge up` applies them.
#   6. BACKOUT: `plunge stack export`, stock `pulumi stack import` on a fresh stock backend, and stock
#      `pulumi preview --refresh` plans only tags updates removing plunge-* keys.
#
# Read the verdict lines and the recorded JSON in $OUT/adopt-<program>/, not the exit code.

source "$(dirname "$0")/lib.sh"

PROG="${1:-aws-yaml}"
PULUMI="${PULUMI:-$(command -v pulumi || true)}"
[ -n "${PULUMI}" ] && [ -x "${PULUMI}" ] || die "stock pulumi not found (set PULUMI)"
CORPUS="${ROOT}/tests/plunge-corpus"
[ -f "${CORPUS}/${PROG}/Pulumi.floci.yaml" ] || die "no corpus program ${PROG} with a floci stack"
# The corpus stacks are encrypted with scripts/plunge-baseline.sh's passphrase.
export PULUMI_CONFIG_PASSPHRASE=plunge-baseline

WORK="${OUT}/adopt-${PROG}"
STOCK_STATE="${WORK}/stock-state"
BACKOUT_STATE="${WORK}/backout-state"
mkdir -p "${WORK}/program" "${STOCK_STATE}" "${BACKOUT_STATE}"
(cd "${CORPUS}/${PROG}" && tar -cf - --exclude node_modules --exclude bin --exclude obj --exclude venv \
  --exclude .venv --exclude __pycache__ --exclude baseline .) | (cd "${WORK}/program" && tar -xf -)
P="${WORK}/program"
PROJECT="$(sed -n 's/^name:[[:space:]]*//p' "${P}/Pulumi.yaml" | head -1)"
RECORDS="${STOCK_STATE}/.pulumi/records/stacks/${PROJECT}/floci"

stock() { (cd "${P}" && PULUMI_BACKEND_URL="file://$1" "${PULUMI}" "${@:2}" --non-interactive); }
plunge() { (cd "${P}" && PULUMI_BACKEND_URL="plunge+file://${STOCK_STATE}" "${PLUNGE}" "$@" --non-interactive); }
tree_digest() { (cd "$1" 2>/dev/null && find . -type f -print0 | sort -z | xargs -0 shasum -a 256 | shasum -a 256) || echo none; }

# only_tag_updates <preview.json> <added|removed>: every non-same step is an update whose diffs are tags/tagsAll,
# and the only tag keys that change are plunge-* keys, added or removed as asked. Prints what it found.
only_tag_updates() {
  python3 -I - "$1" "$2" <<'PY'
import json, sys
raw = open(sys.argv[1]).read()
doc = json.loads(raw[raw.find("{"):])
want = sys.argv[2]
bad, seen = [], 0
for s in doc.get("steps") or []:
    op = s.get("op")
    if op in ("same", "refresh", "read"):
        continue
    urn = s.get("urn", "")
    if op != "update":
        bad.append(f"{op} {urn}")
        continue
    diffs = set(s.get("diffs") or [])
    if not diffs or diffs - {"tags", "tagsAll"}:
        bad.append(f"update {urn} diffs {sorted(diffs)}")
        continue
    old = ((s.get("oldState") or {}).get("inputs") or {}).get("tags") or {}
    new = ((s.get("newState") or {}).get("inputs") or {}).get("tags") or {}
    added = set(new) - set(old)
    removed = set(old) - set(new)
    changed = {k for k in set(old) & set(new) if old[k] != new[k]}
    moved = added if want == "added" else removed
    other = (removed if want == "added" else added) | changed
    if not moved or any(not k.startswith("plunge-") for k in moved) or other:
        bad.append(f"update {urn} added {sorted(added)} removed {sorted(removed)} changed {sorted(changed)}")
        continue
    seen += 1
print(f"{seen} tag updates {want} plunge-* keys; {len(bad)} other changes")
for b in bad:
    print("  " + b)
sys.exit(0 if seen and not bad else 1)
PY
}

echo "== adoption and back-out of ${PROG} (project ${PROJECT})" | tee "${WORK}/run.txt"

# 1. A stock stack.
stock "${STOCK_STATE}" stack init floci --secrets-provider passphrase >"${WORK}/1-stock-init.log" 2>&1 || true
if ! stock "${STOCK_STATE}" up --stack floci --yes >"${WORK}/1-stock-up.log" 2>&1; then
  verdict ADOPT stock-up FAIL "stock pulumi up failed; see 1-stock-up.log"
  exit 1
fi
stock "${STOCK_STATE}" stack export --stack floci --file "${WORK}/1-stock-checkpoint.json" >/dev/null 2>&1 || true

# 2. Gated.
rc=0
plunge preview --stack floci >"${WORK}/2-gated.log" 2>&1 || rc=$?
if [ "${rc}" != 0 ] && grep -q "Adopt it first" "${WORK}/2-gated.log" && [ ! -e "${RECORDS}" ]; then
  verdict ADOPT gated PASS "plunge preview on the stock stack is refused (AdoptionRequiredError) and writes no record"
else
  verdict ADOPT gated FAIL "exit ${rc}; see 2-gated.log"
fi

# 3. Recorded.
rc=0
plunge stack adopt --stack floci --yes >"${WORK}/3-adopt.log" 2>&1 || rc=$?
plunge stack adopt --stack floci --json >"${WORK}/3-adopt.json" 2>/dev/null || true
if [ "${rc}" = 0 ] && [ -s "${RECORDS}/adoption" ]; then
  cp "${RECORDS}/adoption" "${WORK}/3-adoption-record.json"
  verdict ADOPT recorded PASS "plunge stack adopt wrote stacks/${PROJECT}/floci/adoption (3-adoption-record.json)"
else
  verdict ADOPT recorded FAIL "exit ${rc}, adoption record present: $([ -s "${RECORDS}/adoption" ] && echo yes || echo no); see 3-adopt.log"
fi

# 4. Re-run is a no-op.
before="$(tree_digest "${RECORDS}")"
rc=0
plunge stack adopt --stack floci --yes >"${WORK}/4-adopt-again.log" 2>&1 || rc=$?
after="$(tree_digest "${RECORDS}")"
if [ "${rc}" = 0 ] && [ "${before}" = "${after}" ]; then
  verdict ADOPT noop PASS "a second plunge stack adopt left the record tree byte-identical"
else
  verdict ADOPT noop FAIL "exit ${rc}; record tree digest ${before} -> ${after}; see 4-adopt-again.log"
fi

# 5. Markers land as tag updates.
plunge preview --stack floci --json >"${WORK}/5-preview.json" 2>"${WORK}/5-preview.stderr" || true
if out="$(only_tag_updates "${WORK}/5-preview.json" added)"; then
  verdict ADOPT marks PASS "${out}"
else
  verdict ADOPT marks FAIL "$(echo "${out}" | tr '\n' ';')"
fi
echo "${out}" > "${WORK}/5-preview-check.txt"
rc=0
plunge up --stack floci --yes >"${WORK}/5-up.log" 2>&1 || rc=$?
[ "${rc}" = 0 ] || verdict ADOPT marks-up FAIL "plunge up after adoption exited ${rc}; see 5-up.log"

# 6. Back out to stock.
plunge stack export --stack floci --file "${WORK}/6-export.json" >"${WORK}/6-export.log" 2>&1 ||
  { verdict BACKOUT export FAIL "plunge stack export failed; see 6-export.log"; exit 1; }
stock "${BACKOUT_STATE}" stack init floci --secrets-provider passphrase >"${WORK}/6-init.log" 2>&1 || true
stock "${BACKOUT_STATE}" stack import --stack floci --file "${WORK}/6-export.json" >"${WORK}/6-import.log" 2>&1 ||
  { verdict BACKOUT import FAIL "pulumi stack import failed; see 6-import.log"; exit 1; }
stock "${BACKOUT_STATE}" preview --refresh --stack floci --json >"${WORK}/6-preview-refresh.json" \
  2>"${WORK}/6-preview-refresh.stderr" || true
if out="$(only_tag_updates "${WORK}/6-preview-refresh.json" removed)"; then
  verdict BACKOUT preview-refresh PASS "${out}"
else
  verdict BACKOUT preview-refresh FAIL "$(echo "${out}" | tr '\n' ';')"
fi
echo "${out}" > "${WORK}/6-preview-check.txt"

echo "results in ${OUT}"
grep -q ' FAIL ' "${VERDICTS}" && exit 1
exit 0
