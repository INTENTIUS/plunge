#!/usr/bin/env bash
#
# Runs one command with no real cloud credentials in reach, logs it, and fails the run when its output shows
# a real cloud rejecting a call (tests/plunge-corpus/README.md, "Running against the emulators").
#
# Usage:
#   scripts/plunge-safe-run.sh [--log FILE] [--scan DIR]... [--azure-fakes] [-e NAME=VALUE]... -- <command...>
#
#   --log FILE      where the command's stdout and stderr are teed (default: <scratch>/run.log)
#   --scan DIR      also check every file under DIR after the run, for commands that write their output to
#                   files (scripts/plunge-baseline.sh writes each engine's stderr under its OUT). Repeatable.
#                   Each DIR gets a safe-run-verdict.txt holding the verdict line.
#   --azure-fakes   export floci-az's fake ARM identity (the one the corpus stacks and
#                   scripts/plunge-baseline.sh use), which plunge's ARM listings and the azblob store read
#                   from the environment only
#   -e NAME=VALUE   export NAME after the scrub (an emulator endpoint, PLUNGE_CONFORMANCE_*_URL,
#                   STORAGE_EMULATOR_HOST, ...). Repeatable. GOOGLE_APPLICATION_CREDENTIALS is refused.
#
# Environment:
#   PLUNGE_SAFE_RUN_DIR  scratch directory for the empty config locations and the log
#                        (default: a new directory under $TMPDIR)
#   PULUMI_BACKEND_URL   kept when it is a local backend (file://, plunge+file://, or a plunge+s3/gs/azblob URL
#                        whose endpoint or metadata_host is loopback); any other value is refused. Unset, it is
#                        file://<scratch>/state.
#
# What the command runs with:
#   - every AWS_*, AZURE_*, ARM_*, GOOGLE_*, CLOUDSDK_* and GCE_* variable unset, AWS_PROFILE and
#     GOOGLE_APPLICATION_CREDENTIALS among them, then
#   - AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1 (floci accepts any key; static keys
#     are the first link of the AWS default chain, so it looks no further), AWS_EC2_METADATA_DISABLED=true
#   - AWS_CONFIG_FILE and AWS_SHARED_CREDENTIALS_FILE: empty files in the scratch directory
#   - CLOUDSDK_CONFIG and AZURE_CONFIG_DIR: empty directories there (no gcloud application-default
#     credentials, no az login to fall back on)
#   - GCE_METADATA_HOST=127.0.0.1:9 (a closed loopback port, so no Google metadata-server token)
#   - PULUMI_ACCESS_TOKEN unset, PULUMI_SKIP_UPDATE_CHECK=true, PULUMI_SKIP_CONFIRMATIONS=true, and the
#     PULUMI_BACKEND_URL above
#
# After the command exits, its log (and every --scan DIR) is searched for what a real cloud answers to a fake
# or missing credential: UnrecognizedClientException, InvalidClientTokenId, invalid_grant, AADSTS,
# CREDENTIALS_MISSING, and a 401 on a line naming googleapis.com, amazonaws.com, azure.com or windows.net.
# None of these can come from an emulator, so any hit means some call left this machine. The last line is the
# verdict: "plunge-safe-run: CLEAN" or "plunge-safe-run: REAL-CLOUD CALL ..." with the matching lines.
#
# Exit status: the command's own status when the logs are clean; 3 on any hit, whatever the command returned;
# 2 on a usage error or a refused setting.

set -euo pipefail

die() {
  >&2 echo "plunge-safe-run: $*"
  exit 2
}

usage() {
  >&2 echo "usage: scripts/plunge-safe-run.sh [--log FILE] [--scan DIR]... [--azure-fakes] [-e NAME=VALUE]... -- <command...>"
  exit 2
}

LOG=""
SCANS=()
EXTRA=()
AZURE_FAKES=no
while [ $# -gt 0 ]; do
  case "$1" in
    --log) [ $# -ge 2 ] || usage; LOG="$2"; shift 2 ;;
    --scan) [ $# -ge 2 ] || usage; SCANS+=("$2"); shift 2 ;;
    --azure-fakes) AZURE_FAKES=yes; shift ;;
    -e) [ $# -ge 2 ] || usage; EXTRA+=("$2"); shift 2 ;;
    --) shift; break ;;
    *) usage ;;
  esac
done
[ $# -ge 1 ] || usage

for kv in "${EXTRA[@]+"${EXTRA[@]}"}"; do
  case "${kv}" in
    GOOGLE_APPLICATION_CREDENTIALS=*) die "-e GOOGLE_APPLICATION_CREDENTIALS is refused: no credentials JSON may be in reach of a run" ;;
    [A-Za-z_]*=*) ;;
    *) die "-e ${kv} is not NAME=VALUE" ;;
  esac
done

# A backend URL the run may keep: local files, or a bucket store on a loopback emulator.
backend_is_local() {
  local u="$1"
  case "${u}" in
    file://*|plunge+file://*) return 0 ;;
    plunge+s3://*|plunge+azblob://*)
      printf '%s' "${u}" | grep -qE '[?&](endpoint=https?(://|%3A%2F%2F)(localhost|127\.0\.0\.1)([:/%]|$|&)|metadata_host=(localhost|127\.0\.0\.1)(:|%3A))'
      return
      ;;
    plunge+gs://*)
      case "${STORAGE_EMULATOR_HOST:-}" in
        localhost:[0-9]*|127.0.0.1:[0-9]*) return 0 ;;
      esac
      return 1
      ;;
  esac
  return 1
}

if [ -n "${PULUMI_BACKEND_URL:-}" ] && ! backend_is_local "${PULUMI_BACKEND_URL}"; then
  die "PULUMI_BACKEND_URL=${PULUMI_BACKEND_URL} is not a local backend (file://, plunge+file://, or a plunge store on a loopback emulator); unset it to get a scratch one"
fi

SCRATCH="${PLUNGE_SAFE_RUN_DIR:-}"
if [ -z "${SCRATCH}" ]; then
  SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/plunge-safe-run.XXXXXX")"
fi
mkdir -p "${SCRATCH}"
SCRATCH="$(cd "${SCRATCH}" && pwd)"
LOG="${LOG:-${SCRATCH}/run.log}"
mkdir -p "$(dirname "${LOG}")"

# The empty config locations. A fresh name per run, so nothing a previous run left behind is read.
RUN="$(mktemp -d "${SCRATCH}/env.XXXXXX")"
mkdir -p "${RUN}/gcloud" "${RUN}/azure" "${RUN}/state"
: > "${RUN}/aws-config"
: > "${RUN}/aws-credentials"

for v in $(env | sed -n 's/^\(AWS_[A-Za-z0-9_]*\)=.*/\1/p;s/^\(AZURE_[A-Za-z0-9_]*\)=.*/\1/p;s/^\(ARM_[A-Za-z0-9_]*\)=.*/\1/p;s/^\(GOOGLE_[A-Za-z0-9_]*\)=.*/\1/p;s/^\(CLOUDSDK_[A-Za-z0-9_]*\)=.*/\1/p;s/^\(GCE_[A-Za-z0-9_]*\)=.*/\1/p'); do
  unset "${v}"
done
unset PULUMI_ACCESS_TOKEN

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1 AWS_EC2_METADATA_DISABLED=true
export AWS_CONFIG_FILE="${RUN}/aws-config" AWS_SHARED_CREDENTIALS_FILE="${RUN}/aws-credentials"
export CLOUDSDK_CONFIG="${RUN}/gcloud" AZURE_CONFIG_DIR="${RUN}/azure"
export GCE_METADATA_HOST=127.0.0.1:9
export PULUMI_SKIP_UPDATE_CHECK=true PULUMI_SKIP_CONFIRMATIONS=true
export PULUMI_BACKEND_URL="${PULUMI_BACKEND_URL:-file://${RUN}/state}"

if [ "${AZURE_FAKES}" = yes ]; then
  export ARM_SUBSCRIPTION_ID=00000000-0000-0000-0000-00000000c0de
  export ARM_TENANT_ID=00000000-0000-0000-0000-0000000071e7
  export ARM_CLIENT_ID=00000000-0000-0000-0000-00000000c11e
  export ARM_CLIENT_SECRET=floci-az-does-not-check-this
  export ARM_USE_CLI=false ARM_USE_MSI=false ARM_USE_OIDC=false ARM_USE_AKS_WORKLOAD_IDENTITY=false
fi

for kv in "${EXTRA[@]+"${EXTRA[@]}"}"; do
  export "${kv?}"
done

{
  echo "plunge-safe-run: $(date -u +%Y-%m-%dT%H:%M:%SZ) in $(pwd)"
  printf 'plunge-safe-run: command:'
  printf ' %q' "$@"
  echo
  echo "plunge-safe-run: PULUMI_BACKEND_URL=${PULUMI_BACKEND_URL} CLOUDSDK_CONFIG=${CLOUDSDK_CONFIG} AZURE_CONFIG_DIR=${AZURE_CONFIG_DIR}"
} | tee -a "${LOG}" >&2

set +e
"$@" 2>&1 | tee -a "${LOG}"
status="${PIPESTATUS[0]}"
set -e

# The rejection markers. The first group is what AWS, Google and Entra say to a credential they don't
# accept; the second is a 401 from a public cloud host.
MARKERS='UnrecognizedClientException|InvalidClientTokenId|invalid_grant|AADSTS[0-9]*|CREDENTIALS_MISSING'
HOSTS='googleapis\.com|amazonaws\.com|azure\.com|windows\.net'

# hits <file...> prints file:line:text for every marker hit in the files.
hits() {
  {
    grep -HnE "${MARKERS}" "$@" 2>/dev/null || true
    grep -HnE '(^|[^0-9])401([^0-9]|$)' "$@" 2>/dev/null | grep -E "${HOSTS}" || true
  } | sort -u
}

found="$(hits "${LOG}")"
for d in "${SCANS[@]+"${SCANS[@]}"}"; do
  if [ ! -d "${d}" ]; then
    found="${found}${found:+
}${d}: --scan directory does not exist after the run, so it could not be checked"
    continue
  fi
  # The log may sit inside a scanned directory; it was checked above.
  files=()
  while IFS= read -r -d '' f; do
    [ "${f}" -ef "${LOG}" ] || files+=("${f}")
  done < <(find "${d}" -type f ! -name safe-run-verdict.txt -print0)
  if [ "${#files[@]}" -gt 0 ]; then
    h="$(hits "${files[@]}")"
    [ -n "${h}" ] && found="${found}${found:+
}${h}"
  fi
done

if [ -n "${found}" ]; then
  verdict="plunge-safe-run: REAL-CLOUD CALL: the output shows a public cloud rejecting a credential, so a call left this machine. Stop, find the endpoint that was not pointed at an emulator, and do not use this run's results."
  {
    echo "${verdict}"
    printf '%s\n' "${found}" | sed 's/^/  /'
  } | tee -a "${LOG}" >&2
  for d in "${SCANS[@]+"${SCANS[@]}"}"; do
    [ -d "${d}" ] && echo "REAL-CLOUD CALL (see ${LOG})" > "${d}/safe-run-verdict.txt"
  done
  exit 3
fi

for d in "${SCANS[@]+"${SCANS[@]}"}"; do
  echo "CLEAN" > "${d}/safe-run-verdict.txt"
done
echo "plunge-safe-run: CLEAN (command exited ${status}; log ${LOG})" | tee -a "${LOG}" >&2
exit "${status}"
