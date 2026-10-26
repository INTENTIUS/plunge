#!/usr/bin/env bash
#
# Merges upstream pulumi/pulumi master into a fresh branch off plunge's integration branch
# (INTENTIUS/plunge#5, P-L2). The scheduled plunge-upstream-merge workflow runs it; a maintainer can run it
# by hand the same way.
#
# Usage:
#   scripts/plunge-upstream-merge.sh [--push]
#
# Steps:
#   1. Fetch upstream master (fetch only: this script never pushes to pulumi/pulumi, and a remote it
#      adds gets a push URL that can't work).
#   2. Create upstream-merge/<UTC date>-<upstream short sha> off origin/$BASE, and merge upstream/master
#      into it with a merge commit (--no-ff, no fast-forward, no squash).
#   3. Take upstream's generated Automation API (GENERATED_DIRS below: the Go package, and the nodejs and
#      python interfaces) as it is at upstream/master, conflicted or not. Those files are upstream's
#      generated output; plunge's own commands live in fork-owned files beside them (FORK_OWNED below:
#      plunge_commands.go, optplunge*, nodejs automation/plunge.ts, python automation/plunge.py,
#      INTENTIUS/plunge#21), which this step never touches. A conflict confined to the generated files is
#      resolved this way and the merge goes on; the report lists the files it took.
#      Any other conflict: record the conflicted files, abort the merge, and stop with CONFLICT. A human
#      resolves it (the report says how to reproduce it).
#   4. Move any workflow upstream added to .github/workflows into .github/upstream-workflows/, in its own
#      commit, so nothing upstream schedules or publishes becomes live (the plunge-upstream-guard check
#      would fail the branch otherwise).
#   5. Rebuild: go build ./... in sdk (which builds plunge_commands.go against upstream's regenerated
#      package) and pkg, then bin/plunge with plunge's version stamp. A failure stops with BUILD-FAILED.
#   6. With --push, push the branch to origin. Only upstream-merge/* branches are ever pushed; never main
#      or $BASE. Merging the branch into $BASE is a human's call.
#
# Environment:
#   BASE      integration branch to branch from   (default: main)
#   UPSTREAM  upstream remote name                (default: upstream)
#   OUT       report directory                    (default: plunge-upstream-merge-out)
#
# Output in $OUT:
#   report.md     the report the workflow posts as an issue
#   verdict.txt   one line:
#     UPSTREAM-MERGE UP-TO-DATE <upstream sha>              nothing new upstream
#     UPSTREAM-MERGE MERGED <branch> <upstream sha>         merged and built
#     UPSTREAM-MERGE CONFLICT <branch> <upstream sha> <n>   n files conflict; nothing was committed
#     UPSTREAM-MERGE BUILD-FAILED <branch> <upstream sha>   merged, but the build failed (see build.log)
#     UPSTREAM-MERGE ERROR <reason>
#   conflicts.txt, generated-taken.txt, build.log, moved-workflows.txt as they apply
#   branch.txt    the branch name
#
# Read verdict.txt, not the exit code. Exit codes: 0 UP-TO-DATE or MERGED, 1 CONFLICT or BUILD-FAILED,
# 2 ERROR.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "${ROOT}"

BASE="${BASE:-main}"
UPSTREAM="${UPSTREAM:-upstream}"
UPSTREAM_URL="https://github.com/pulumi/pulumi.git"
OUT="${OUT:-plunge-upstream-merge-out}"
PUSH=0

for arg in "$@"; do
  case "${arg}" in
    --push) PUSH=1 ;;
    *)
      >&2 echo "usage: $0 [--push]"
      exit 2
      ;;
  esac
done

mkdir -p "${OUT}"
OUT="$(cd "${OUT}" && pwd)"
REPORT="${OUT}/report.md"
VERDICT="${OUT}/verdict.txt"
: > "${REPORT}"

verdict() {
  echo "UPSTREAM-MERGE $*" | tee "${VERDICT}"
}

fail() {
  verdict "ERROR $*"
  printf '\nError: %s\n' "$*" >> "${REPORT}"
  exit 2
}

report() {
  printf '%s\n' "$@" >> "${REPORT}"
}

# Upstream's generated Automation API, which plunge never edits (INTENTIUS/plunge#21, #24).
GENERATED_DIRS=(
  sdk/go/auto/automation
  sdk/nodejs/automation/interface
  sdk/python/lib/pulumi/automation/interface
)
# plunge's own files inside or beside GENERATED_DIRS. The generators never write these.
FORK_OWNED='^sdk/go/auto/automation/(plunge_[^/]*\.go|optplunge[^/]*/.*)$'

is_generated() {
  local f="$1" d
  if [[ "${f}" =~ ${FORK_OWNED} ]]; then
    return 1
  fi
  for d in "${GENERATED_DIRS[@]}"; do
    case "${f}" in "${d}"/*) return 0 ;; esac
  done
  return 1
}

# take_generated <rev>: make every generated file in the index and work tree what it is at <rev>.
# Files <rev> lacks are removed; fork-owned files are left alone. Prints the paths it changed.
take_generated() {
  local rev="$1" f
  while IFS= read -r f; do
    if is_generated "${f}" && ! git cat-file -e "${rev}:${f}" 2> /dev/null; then
      git rm -q --force -- "${f}"
    fi
  done < <(git ls-files -- "${GENERATED_DIRS[@]}" | sort -u)
  for f in "${GENERATED_DIRS[@]}"; do
    if git cat-file -e "${rev}:${f}" 2> /dev/null; then
      git checkout "${rev}" -- "${f}"
    fi
  done
  git diff --cached --name-only HEAD -- "${GENERATED_DIRS[@]}"
}

if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  fail "the working tree has uncommitted changes"
fi

# Upstream is fetched from pulumi/pulumi and nothing else. If the remote is missing, add it with a push
# URL that refuses, so no later command can push there by mistake.
if ! git remote get-url "${UPSTREAM}" > /dev/null 2>&1; then
  git remote add "${UPSTREAM}" "${UPSTREAM_URL}"
  git remote set-url --push "${UPSTREAM}" "no-push://upstream-is-fetch-only"
fi
case "$(git remote get-url "${UPSTREAM}")" in
  https://github.com/pulumi/pulumi|https://github.com/pulumi/pulumi.git|git@github.com:pulumi/pulumi.git) ;;
  *) fail "remote ${UPSTREAM} is $(git remote get-url "${UPSTREAM}"), not pulumi/pulumi" ;;
esac

git fetch --no-tags origin "${BASE}" || fail "fetching origin ${BASE}"
git fetch --no-tags "${UPSTREAM}" master || fail "fetching ${UPSTREAM} master"

base_sha="$(git rev-parse "origin/${BASE}")"
up_sha="$(git rev-parse "${UPSTREAM}/master")"
up_short="$(git rev-parse --short=10 "${up_sha}")"
compat_before="$(git show "${base_sha}:CHANGELOG.md" | sed -n 's/^## \([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | head -n 1)"

report "# Upstream merge: pulumi/pulumi master ${up_short} into ${BASE}" ""
report "- ${BASE}: $(git rev-parse --short=10 "${base_sha}")"
report "- upstream master: ${up_short} ($(git log -1 --format='%cs %s' "${up_sha}"))"
report "- Pulumi-compatible version on ${BASE}: ${compat_before:-unknown}"

if git merge-base --is-ancestor "${up_sha}" "${base_sha}"; then
  report "" "Nothing new upstream: ${up_short} is already in ${BASE}."
  verdict "UP-TO-DATE ${up_short}"
  exit 0
fi

new_commits="$(git rev-list --count "${base_sha}..${up_sha}")"
report "- upstream commits not in ${BASE}: ${new_commits}"

BRANCH="upstream-merge/$(date -u +%Y-%m-%d)-${up_short}"
case "${BRANCH}" in
  main|master|"${BASE}") fail "refusing to use ${BRANCH} as the merge branch" ;;
esac
echo "${BRANCH}" > "${OUT}/branch.txt"
report "- branch: ${BRANCH}" ""

git checkout -q -B "${BRANCH}" "${base_sha}"

msg="Merge pulumi/pulumi master ${up_short} into ${BASE}

Scheduled upstream merge (scripts/plunge-upstream-merge.sh, INTENTIUS/plunge#5)."

merged=1
git merge --no-ff --no-edit -m "${msg}" "${up_sha}" > "${OUT}/merge.log" 2>&1 || merged=0
if [ "${merged}" -eq 0 ]; then
  git diff --name-only --diff-filter=U | sort > "${OUT}/conflicts.txt"
  other=0
  while IFS= read -r f; do
    is_generated "${f}" || other=1
  done < "${OUT}/conflicts.txt"
  if [ -s "${OUT}/conflicts.txt" ] && [ "${other}" -eq 0 ]; then
    # Every conflict is in upstream's generated output: take upstream's and finish the merge.
    take_generated "${up_sha}" > /dev/null
    if [ -z "$(git diff --name-only --diff-filter=U)" ]; then
      git commit -q --no-edit -m "${msg}"
      merged=1
      report "## Generated files taken from upstream" "" \
        "These conflicted only because they are generated; upstream's versions were taken:" ""
      sed 's/^/- /' "${OUT}/conflicts.txt" >> "${REPORT}"
      report ""
    fi
  fi
fi
if [ "${merged}" -eq 0 ]; then
  n="$(wc -l < "${OUT}/conflicts.txt" | tr -d ' ')"
  git merge --abort || true
  git checkout -q --detach "${base_sha}"
  if [ "${n}" -eq 0 ]; then
    tail -n 20 "${OUT}/merge.log" >&2
    fail "git merge failed without conflicts (see merge.log)"
  fi
  report "## Conflicts" "" \
    "The merge stopped on ${n} conflicted files. Nothing was committed or pushed; a human resolves it." ""
  sed 's/^/- /' "${OUT}/conflicts.txt" >> "${REPORT}"
  report "" "To reproduce:" "" '```sh' \
    "git fetch origin ${BASE} && git fetch ${UPSTREAM} master" \
    "git checkout -b ${BRANCH} origin/${BASE}" \
    "git merge --no-ff ${up_sha}" \
    '```' "" \
    "Resolve, then check .github/workflows (only plunge-* files may stay there), rebuild, and push the branch."
  verdict "CONFLICT ${BRANCH} ${up_short} ${n}"
  exit 1
fi

# A clean merge can still leave plunge's generated files apart from upstream's (a hand edit, or a file
# upstream deleted that plunge changed). Bring them to upstream's in their own commit.
taken="$(take_generated "${up_sha}")"
if [ -n "${taken}" ]; then
  echo "${taken}" > "${OUT}/generated-taken.txt"
  git commit -q -m "Take upstream's generated Automation API at ${up_short}"
  report "## Generated files brought to upstream's" "" \
    "plunge's copies differed from upstream's generated output; upstream's were taken:" ""
  sed 's/^/- /' "${OUT}/generated-taken.txt" >> "${REPORT}"
  report ""
fi

# A workflow upstream added lands live in .github/workflows. Move it next to the others.
moved="$(find .github/workflows -maxdepth 1 -type f \( -name '*.yml' -o -name '*.yaml' \) ! -name 'plunge-*' | sort)"
if [ -n "${moved}" ]; then
  mkdir -p .github/upstream-workflows
  echo "${moved}" > "${OUT}/moved-workflows.txt"
  while IFS= read -r f; do
    git mv "${f}" ".github/upstream-workflows/$(basename "${f}")"
  done <<< "${moved}"
  git commit -q -m "Move upstream workflows out of .github/workflows after merging ${up_short}"
  report "## Workflows moved" "" "Upstream added these; they were moved to .github/upstream-workflows/:" ""
  sed 's/^/- /' "${OUT}/moved-workflows.txt" >> "${REPORT}"
  report ""
fi

compat_after="$(./scripts/plunge-version.sh pulumi || true)"
report "- Pulumi-compatible version after the merge: ${compat_after:-unknown}" ""

build_ok=1
{
  echo "== go build ./... (sdk)"
  (cd sdk && go build ./...)
  echo "== go build ./... (pkg)"
  (cd pkg && go build ./...)
  echo "== bin/plunge"
  (cd pkg && go build -o ../bin/plunge -ldflags "$(../scripts/plunge-version.sh ldflags)" ./cmd/pulumi)
  ./bin/plunge version
} > "${OUT}/build.log" 2>&1 || build_ok=0

if [ "${build_ok}" -eq 1 ]; then
  report "## Build" "" "go build in sdk and pkg, and bin/plunge, all built. Tests were not run." ""
  status="MERGED ${BRANCH} ${up_short}"
  code=0
else
  report "## Build" "" "The build failed. The last lines of build.log:" "" '```'
  tail -n 40 "${OUT}/build.log" >> "${REPORT}"
  report '```' ""
  status="BUILD-FAILED ${BRANCH} ${up_short}"
  code=1
fi

if [ "${PUSH}" -eq 1 ]; then
  case "${BRANCH}" in
    upstream-merge/*) ;;
    *) fail "refusing to push ${BRANCH}" ;;
  esac
  git push origin "refs/heads/${BRANCH}:refs/heads/${BRANCH}" || fail "pushing ${BRANCH} to origin"
  report "Pushed ${BRANCH} to origin. Nothing was merged into ${BASE} or main; that's a human's call:" "" \
    '```sh' \
    "git checkout ${BASE} && git merge --no-ff origin/${BRANCH}" \
    '```'
else
  report "Not pushed (run with --push to push ${BRANCH} to origin)."
fi

verdict "${status}"
exit "${code}"
