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
#   1. Fetch upstream master (fetch only: this script never pushes to pulumi/pulumi, and the upstream
#      remote's push URL is set to one that can't work, on a remote it adds and on one already there,
#      INTENTIUS/plunge#33).
#   2. Create upstream-merge/<UTC date>-<upstream short sha> off origin/$BASE, and merge upstream/master
#      into it with a merge commit (--no-ff, no fast-forward, no squash).
#   3. Take upstream's generated Automation API (GENERATED_DIRS below: the Go package, and the nodejs and
#      python interfaces) as it is at upstream/master, conflicted or not. Those files are upstream's
#      generated output; plunge's own commands live in fork-owned files beside them (FORK_OWNED below:
#      plunge_commands.go, optplunge*, nodejs automation/plunge.ts, python automation/plunge.py,
#      INTENTIUS/plunge#21), which this step never touches. A conflict confined to the generated files is
#      resolved this way and the merge goes on; the report lists the files it took.
#      A conflict in a go.mod or go.sum is resolved too (INTENTIUS/plunge#26): the script takes upstream's
#      file, so the merge picks up upstream's dependency bumps, re-applies what plunge adds to that module
#      (GO_MOD_FORK below, a fork-owned list; versions come from the fork's go.mod on $BASE), and runs
#      `go mod tidy` in the module. Then it tidies every other module in the repo too (INTENTIUS/plunge#30):
#      a module that merged cleanly but replaces ../pkg or ../sdk (sdk/pcl, a language host, a test
#      program) can be left untidy by the resolved go.mod. Modules go in the order sdk, pkg, sdk/pcl, the
#      language hosts, then the rest; scripts/tidy.sh's EXCLUDE list is skipped, as `make tidy` skips it.
#      If any tidy fails, the merge stops with CONFLICT and gomod.log says why.
#      Any other conflict: record the conflicted files, abort the merge, and stop with CONFLICT. A human
#      resolves it (the report says how to reproduce it).
#   4. Move any workflow upstream added to .github/workflows into .github/upstream-workflows/, in its own
#      commit, so nothing upstream schedules or publishes becomes live (the plunge-upstream-guard check
#      would fail the branch otherwise).
#   5. Rebuild: go build ./... in sdk (which builds plunge_commands.go against upstream's regenerated
#      package), pkg, sdk/pcl and the three language hosts (INTENTIUS/plunge#30), then bin/plunge with
#      plunge's version stamp. A failure stops with BUILD-FAILED.
#   6. With --push, push the branch to origin. Only upstream-merge/* branches are ever pushed; never main
#      or $BASE. Merging the branch into $BASE is a human's call.
#
# Environment:
#   BASE      integration branch to branch from   (default: main)
#   UPSTREAM  upstream remote name                (default: upstream)
#   OUT       report directory                    (default: plunge-upstream-merge-out)
#   HOST_GOTOOLCHAIN  GOTOOLCHAIN for the language-host modules (default: go1.27.1)
#
# Output in $OUT:
#   report.md     the report the workflow posts as an issue
#   verdict.txt   one line:
#     UPSTREAM-MERGE UP-TO-DATE <upstream sha>              nothing new upstream
#     UPSTREAM-MERGE MERGED <branch> <upstream sha>         merged and built
#     UPSTREAM-MERGE CONFLICT <branch> <upstream sha> <n>   n files conflict; nothing was committed
#     UPSTREAM-MERGE BUILD-FAILED <branch> <upstream sha>   merged, but the build failed (see build.log)
#     UPSTREAM-MERGE ERROR <reason>
#   conflicts.txt, generated-taken.txt, gomod.log, build.log, moved-workflows.txt as they apply
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
HOST_GOTOOLCHAIN="${HOST_GOTOOLCHAIN:-go1.27.1}"
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

# The language hosts' modules, which build and tidy with HOST_GOTOOLCHAIN (INTENTIUS/plunge#22, #30).
LANGUAGE_HOSTS=(
  sdk/go/pulumi-language-go
  sdk/nodejs/cmd/pulumi-language-nodejs
  sdk/python/cmd/pulumi-language-python
)

is_language_host() {
  local h
  for h in "${LANGUAGE_HOSTS[@]}"; do
    [ "$1" = "${h}" ] && return 0
  done
  return 1
}

# in_module <dir> <command...>: run a command in a module's directory, with HOST_GOTOOLCHAIN in a
# language host.
in_module() {
  local dir="$1"
  shift
  if is_language_host "${dir}"; then
    (cd "${dir}" && GOTOOLCHAIN="${HOST_GOTOOLCHAIN}" "$@")
  else
    (cd "${dir}" && "$@")
  fi
}

# order_modules: read module dirs on stdin, print them sdk, pkg, sdk/pcl, the language hosts, then the
# rest sorted. The others read sdk's and pkg's go.mod through a replace, so those go first.
order_modules() {
  local dir rank
  sort -u | while IFS= read -r dir; do
    case "${dir}" in
      sdk) rank=0 ;;
      pkg) rank=1 ;;
      sdk/pcl) rank=2 ;;
      *) if is_language_host "${dir}"; then rank=3; else rank=4; fi ;;
    esac
    echo "${rank} ${dir}"
  done | sort -k1,1n -k2 | cut -d' ' -f2-
}

# all_modules: every tracked module dir, less the go.mod files scripts/tidy.sh excludes (their
# dependencies are generated and gitignored), in order_modules' order.
all_modules() {
  local exclude
  exclude="$(sed -n 's/^EXCLUDE="\(.*\)"$/\1/p' scripts/tidy.sh 2> /dev/null || true)"
  git ls-files -- 'go.mod' '*/go.mod' | {
    if [ -n "${exclude}" ]; then
      # shellcheck disable=SC2086 # EXCLUDE is a list of `-e <pattern>` arguments.
      grep -v ${exclude} || true
    else
      cat
    fi
  } | while IFS= read -r f; do dirname "${f}"; done | order_modules
}

# tidy_module <dir>: scripts/tidy.sh's flags; tidy.sh --check rejects toolchain lines, so drop any tidy
# adds. Stages the module's go.mod and go.sum.
tidy_module() {
  local dir="$1"
  echo "== ${dir}: go mod tidy"
  in_module "${dir}" go mod tidy -compat=1.23 || return 1
  in_module "${dir}" go mod edit -toolchain=none || return 1
  git add -- "${dir}/go.mod" || return 1
  if [ -f "${dir}/go.sum" ]; then
    git add -- "${dir}/go.sum" || return 1
  fi
}

# What plunge adds to upstream's go.mod files, by module (INTENTIUS/plunge#26).
GO_MOD_FORK=scripts/plunge-go-mod-fork.txt

is_go_mod() {
  case "$1" in go.mod|go.sum|*/go.mod|*/go.sum) return 0 ;; esac
  return 1
}

# version_lt <a> <b>: true when version a sorts before b. Takes module versions (v1.2.3, pseudo-versions,
# +incompatible) and go directives (1.27.1). Pre-release parts compare as strings, which orders
# pseudo-versions by their timestamp.
version_lt() {
  local a="${1#v}" b="${2#v}" ap="" bp="" i
  local -a x y
  a="${a%%+*}"
  b="${b%%+*}"
  if [[ "${a}" == *-* ]]; then ap="${a#*-}"; fi
  if [[ "${b}" == *-* ]]; then bp="${b#*-}"; fi
  IFS=. read -ra x <<< "${a%%-*}"
  IFS=. read -ra y <<< "${b%%-*}"
  for i in 0 1 2; do
    if [ "$((10#${x[i]:-0}))" -ne "$((10#${y[i]:-0}))" ]; then
      [ "$((10#${x[i]:-0}))" -lt "$((10#${y[i]:-0}))" ]
      return
    fi
  done
  # Same release: a pre-release sorts before the release itself.
  if [ -z "${ap}" ]; then return 1; fi
  if [ -z "${bp}" ]; then return 0; fi
  [ "${ap}" != "${bp}" ] && [ "$(printf '%s\n%s\n' "${ap}" "${bp}" | LC_ALL=C sort | head -n 1)" = "${ap}" ]
}

# mod_require <go.mod> <path>: the version the file requires of <path>, if any.
mod_require() {
  awk -v p="$2" '
    /^require \(/ { blk = 1; next }
    /^\)/ { blk = 0; next }
    /^require / && $2 == p { print $3; exit }
    blk && $1 == p { print $2; exit }
  ' "$1"
}

# mod_replace <go.mod> <path>: the file's replace of <path> as old[@v]=new[@v], if any.
mod_replace() {
  awk -v p="$2" '
    /^replace \(/ { blk = 1; next }
    /^\)/ { blk = 0; next }
    /^replace / { sub(/^replace /, ""); one = 1 }
    (blk || one) && $1 == p {
      old = $1; i = 2
      if ($i != "=>") { old = old "@" $i; i++ }
      new = $(i + 1)
      if ($(i + 2) != "") { new = new "@" $(i + 2) }
      print old "=" new
      exit
    }
    { one = 0 }
  ' "$1"
}

# fix_go_mod <module dir> <base go.mod>: re-apply GO_MOD_FORK's entries for the module to its go.mod
# (upstream's by now), taking versions from <base go.mod>, then tidy the module. Logs to stdout.
fix_go_mod() {
  local dir="$1" base_mod="$2" mod="$1/go.mod" d kind path want have
  while read -r d kind path; do
    [ "${d}" = "${dir}" ] || continue
    case "${kind}" in
      go)
        want="$(sed -n 's/^go //p' "${base_mod}")"
        have="$(sed -n 's/^go //p' "${mod}")"
        if [ -n "${want}" ] && version_lt "${have}" "${want}"; then
          echo "== ${dir}: go ${have} -> ${want}"
          go mod edit -go="${want}" "${mod}" || return 1
        fi
        ;;
      require)
        want="$(mod_require "${base_mod}" "${path}")"
        have="$(mod_require "${mod}" "${path}")"
        if [ -z "${want}" ]; then
          echo "== ${dir}: ${path} is listed in ${GO_MOD_FORK} but ${BASE}'s go.mod doesn't require it; skipped"
        elif [ -z "${have}" ] || version_lt "${have}" "${want}"; then
          echo "== ${dir}: require ${path} ${have:-(none)} -> ${want}"
          go mod edit -require="${path}@${want}" "${mod}" || return 1
        fi
        ;;
      replace)
        want="$(mod_replace "${base_mod}" "${path}")"
        if [ -z "${want}" ]; then
          echo "== ${dir}: ${path} is listed in ${GO_MOD_FORK} but ${BASE}'s go.mod doesn't replace it; skipped"
        else
          echo "== ${dir}: replace ${want}"
          go mod edit -dropreplace="${path}" -replace="${want}" "${mod}" || return 1
        fi
        ;;
      *)
        echo "${GO_MOD_FORK}: unknown entry '${d} ${kind} ${path}'"
        return 1
        ;;
    esac
  done < <(sed -e 's/#.*//' -e '/^[[:space:]]*$/d' "${GO_MOD_FORK}")
  tidy_module "${dir}"
}

# resolve_go_mods <rev> <conflicts file>: take <rev>'s copy of each conflicted go.mod and go.sum, then fix
# and tidy each conflicted module, then tidy every other module (INTENTIUS/plunge#30), all in
# order_modules' order. Fails if a module can't be fixed or tidied.
resolve_go_mods() {
  local rev="$1" f dir base_mod
  local -a done_dirs=()
  while IFS= read -r f; do
    if is_go_mod "${f}"; then
      git checkout "${rev}" -- "${f}" || return 1
    fi
  done < "$2"
  while IFS= read -r dir; do
    base_mod="${OUT}/base-go.mod"
    git show "${base_sha}:${dir}/go.mod" > "${base_mod}" 2> /dev/null || : > "${base_mod}"
    fix_go_mod "${dir}" "${base_mod}" || return 1
    done_dirs+=("${dir}")
  done < <(while IFS= read -r f; do
             if is_go_mod "${f}"; then dirname "${f}"; fi
           done < "$2" | order_modules)
  # A module that merged cleanly can still need tidying once the resolved go.mod files are in.
  while IFS= read -r dir; do
    if [ "${#done_dirs[@]}" -gt 0 ] && printf '%s\n' "${done_dirs[@]}" | grep -qxF -- "${dir}"; then
      continue
    fi
    tidy_module "${dir}" || return 1
  done < <(all_modules)
}

if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  fail "the working tree has uncommitted changes"
fi

# Upstream is fetched from pulumi/pulumi and nothing else. If the remote is missing, add it. Either way,
# give it a push URL that refuses, so no later command (a `git push upstream`, or a tag pushed there) can
# push there by mistake (INTENTIUS/plunge#33).
NO_PUSH_URL="no-push://upstream-is-fetch-only"
push_url_set=0
if ! git remote get-url "${UPSTREAM}" > /dev/null 2>&1; then
  git remote add "${UPSTREAM}" "${UPSTREAM_URL}"
fi
case "$(git remote get-url "${UPSTREAM}")" in
  https://github.com/pulumi/pulumi|https://github.com/pulumi/pulumi.git|git@github.com:pulumi/pulumi.git) ;;
  *) fail "remote ${UPSTREAM} is $(git remote get-url "${UPSTREAM}"), not pulumi/pulumi" ;;
esac
if [ "$(git remote get-url --push --all "${UPSTREAM}")" != "${NO_PUSH_URL}" ]; then
  # Drop every push URL the remote has (set-url --push alone replaces only the first), then set the one.
  git config --unset-all "remote.${UPSTREAM}.pushurl" || true
  git remote set-url --push "${UPSTREAM}" "${NO_PUSH_URL}" || fail "setting ${UPSTREAM}'s push URL"
  push_url_set=1
fi

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
if [ "${push_url_set}" -eq 1 ]; then
  report "- set remote ${UPSTREAM}'s push URL to ${NO_PUSH_URL} (it could push before)"
fi

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
  : > "${OUT}/generated-conflicts.txt"
  : > "${OUT}/gomod-conflicts.txt"
  while IFS= read -r f; do
    if is_generated "${f}"; then
      echo "${f}" >> "${OUT}/generated-conflicts.txt"
    elif is_go_mod "${f}" && git cat-file -e "${up_sha}:${f}" 2> /dev/null; then
      # A go.mod or go.sum upstream deleted is a human's call; one upstream still has is resolved.
      echo "${f}" >> "${OUT}/gomod-conflicts.txt"
    else
      other=1
    fi
  done < "${OUT}/conflicts.txt"
  if [ -s "${OUT}/conflicts.txt" ] && [ "${other}" -eq 0 ]; then
    # Every conflict is in upstream's generated output or in a go.mod or go.sum: take upstream's, re-apply
    # plunge's own module requirements, and finish the merge.
    if [ -s "${OUT}/generated-conflicts.txt" ]; then
      take_generated "${up_sha}" > /dev/null
    fi
    gomod_ok=1
    if [ -s "${OUT}/gomod-conflicts.txt" ]; then
      resolve_go_mods "${up_sha}" "${OUT}/gomod-conflicts.txt" > "${OUT}/gomod.log" 2>&1 || gomod_ok=0
    fi
    if [ "${gomod_ok}" -eq 0 ]; then
      report "## go.mod resolution failed" "" \
        "The go.mod and go.sum conflicts couldn't be resolved automatically. The last lines of gomod.log:" "" \
        '```'
      tail -n 40 "${OUT}/gomod.log" >> "${REPORT}"
      report '```' ""
    elif [ -z "$(git diff --name-only --diff-filter=U)" ]; then
      git commit -q --no-edit -m "${msg}"
      merged=1
      if [ -s "${OUT}/generated-conflicts.txt" ]; then
        report "## Generated files taken from upstream" "" \
          "These conflicted only because they are generated; upstream's versions were taken:" ""
        sed 's/^/- /' "${OUT}/generated-conflicts.txt" >> "${REPORT}"
        report ""
      fi
      if [ -s "${OUT}/gomod-conflicts.txt" ]; then
        report "## go.mod and go.sum taken from upstream" "" \
          "These conflicted; upstream's versions were taken, plunge's own entries from ${GO_MOD_FORK}" \
          "re-applied, and \`go mod tidy\` run in every module (gomod.log has the details):" ""
        sed 's/^/- /' "${OUT}/gomod-conflicts.txt" >> "${REPORT}"
        report "" '```'
        grep '^== ' "${OUT}/gomod.log" | grep -v ': go mod tidy$' | sed 's/^== //' >> "${REPORT}" || true
        report '```' ""
      fi
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

# sdk, pkg, sdk/pcl and the language hosts (INTENTIUS/plunge#30). After a go.mod resolution every module
# was tidied above; these are the ones plunge ships.
BUILD_MODULES=(sdk pkg sdk/pcl "${LANGUAGE_HOSTS[@]}")
# Each step is checked on its own: errexit doesn't apply inside a block whose status is tested.
build_ok=1
{
  for dir in "${BUILD_MODULES[@]}"; do
    echo "== go build ./... (${dir})"
    in_module "${dir}" go build ./... || build_ok=0
  done
  echo "== bin/plunge"
  if (cd pkg && go build -o ../bin/plunge -ldflags "$(../scripts/plunge-version.sh ldflags)" ./cmd/pulumi); then
    ./bin/plunge version || build_ok=0
  else
    build_ok=0
  fi
} > "${OUT}/build.log" 2>&1

if [ "${build_ok}" -eq 1 ]; then
  report "## Build" "" "go build in ${BUILD_MODULES[*]}, and bin/plunge, all built. Tests were not run." ""
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
