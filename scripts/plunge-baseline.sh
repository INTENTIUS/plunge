#!/usr/bin/env bash
#
# Compares `plunge preview` with stock `pulumi preview` on the plunge corpus (INTENTIUS/plunge#3, P-L0).
#
# The maintainer runs this by hand. It talks only to local emulators: every program's stack config must
# point its cloud endpoints at localhost, and real cloud credentials are scrubbed from the environment.
#
# Usage:
#   scripts/plunge-baseline.sh [program ...]
#
# Programs are directory names under the corpus (default: every directory there with a Pulumi.yaml).
#
# Environment:
#   PLUNGE          plunge binary                  (default: bin/plunge)
#   PULUMI          stock pulumi binary            (default: pulumi on PATH)
#   CORPUS          corpus directory               (default: tests/plunge-corpus)
#   STACK           stack name in each program     (default: floci, i.e. Pulumi.floci.yaml)
#   OUT             output directory               (default: plunge-baseline-out/<UTC timestamp>)
#   PULUMI_CONFIG_PASSPHRASE                       (default: plunge-baseline)
#   FLOCI_AZ_HOST   floci-az's host:port, loopback only (default: localhost:4577); read only when an
#                   Azure program is selected
#   FLOCI_AZ_CA_FILE  PEM file floci-az's TLS certificate verifies against (default: fetched from
#                   http://$FLOCI_AZ_HOST/_floci/tls-cert into $OUT/floci-az-ca.pem)
#
# Each engine gets its own copy of the program and its own file backend under $OUT, so the two runs
# share nothing but the plugin cache in PULUMI_HOME. Each runs with its own binary's directory first on
# PATH, so plunge uses the language hosts shipped beside plunge and pulumi the ones beside pulumi.
#
# Output, per program, in $OUT/<program>/:
#   <engine>.preview.json      raw `preview --json`
#   <engine>.stderr            the engine's stderr
#   <engine>.normalized.json   steps (op, urn, type, inputs) sorted by URN, plus the change summary
#   compare.txt                the comparison
# and $OUT/verdicts.txt, one line per program:
#   VERDICT <program> MATCH          same steps, same inputs, same change summary
#   VERDICT <program> DIFF-INPUTS    same steps and summary; some planned inputs differ (see compare.txt;
#                                    autonamed properties carry a random suffix and can differ by design)
#   VERDICT <program> DIFF-STEPS     the step lists or change summaries differ
#   VERDICT <program> ERROR          an engine failed or a safety check refused the program
#
# Read the verdict lines and compare.txt, not the exit code. The exit code is 0 only when every
# program is MATCH or DIFF-INPUTS.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

PLUNGE="${PLUNGE:-${ROOT}/bin/plunge}"
PULUMI="${PULUMI:-$(command -v pulumi || true)}"
CORPUS="${CORPUS:-${ROOT}/tests/plunge-corpus}"
STACK="${STACK:-floci}"
OUT="${OUT:-${ROOT}/plunge-baseline-out/$(date -u +%Y%m%dT%H%M%SZ)}"
export PULUMI_CONFIG_PASSPHRASE="${PULUMI_CONFIG_PASSPHRASE:-plunge-baseline}"

die() {
  >&2 echo "plunge-baseline: $*"
  exit 2
}

[ -x "${PLUNGE}" ] || die "plunge binary not found at ${PLUNGE} (run 'make plunge' or set PLUNGE)"
[ -n "${PULUMI}" ] && [ -x "${PULUMI}" ] || die "stock pulumi not found (set PULUMI)"
[ -d "${CORPUS}" ] || die "corpus not found at ${CORPUS} (set CORPUS)"
command -v python3 >/dev/null || die "python3 is required"
PLUNGE="$(cd "$(dirname "${PLUNGE}")" && pwd)/$(basename "${PLUNGE}")"
PULUMI="$(cd "$(dirname "${PULUMI}")" && pwd)/$(basename "${PULUMI}")"

# Offline: no real cloud credentials reach either engine or any provider. The corpus stacks set fake
# keys in config; these cover anything that reads the environment instead.
for v in $(env | sed -n 's/^\(AWS_[A-Z_]*\)=.*/\1/p;s/^\(AZURE_[A-Z_]*\)=.*/\1/p;s/^\(ARM_[A-Z_]*\)=.*/\1/p;s/^\(GOOGLE_[A-Z_]*\)=.*/\1/p;s/^\(CLOUDSDK_[A-Z_]*\)=.*/\1/p'); do
  unset "${v}"
done
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
export AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_CONFIG_FILE=/dev/null AWS_EC2_METADATA_DISABLED=true
export PULUMI_SKIP_UPDATE_CHECK=true PULUMI_SKIP_CONFIRMATIONS=true
unset PULUMI_ACCESS_TOKEN PULUMI_BACKEND_URL
# pulumi-gcp lists the project's regions when a provider configures, and that lookup ignores every custom
# endpoint, so with floci-gcp it went to compute.googleapis.com (a 401 with the corpus's fake token,
# INTENTIUS/plunge#42). gcp-yaml's stack sets gcp:skipRegionValidation for the default provider; an explicit
# provider can't take it as a property and reads this variable instead.
export PULUMI_GCP_SKIP_REGION_VALIDATION=true

mkdir -p "${OUT}"
# The scrub above unset CLOUDSDK_CONFIG too, which would send gcloud's library back to ~/.config/gcloud and its
# application-default credentials. An empty directory instead: no ADC in reach of any GCP call. The same for the
# Azure CLI's login, for every program (azure_setup below names the same directory).
mkdir -p "${OUT}/.gcloud-no-login" "${OUT}/.azure-no-login"
export CLOUDSDK_CONFIG="${OUT}/.gcloud-no-login" AZURE_CONFIG_DIR="${OUT}/.azure-no-login"
VERDICTS="${OUT}/verdicts.txt"
: > "${VERDICTS}"

{
  echo "plunge: ${PLUNGE}"
  "${PLUNGE}" version 2>&1 | sed 's/^/  /'
  echo "pulumi: ${PULUMI}"
  echo "  $("${PULUMI}" version 2>&1)"
  echo "corpus: ${CORPUS} @ $(git -C "${ROOT}" rev-parse HEAD 2>/dev/null || echo unknown)"
  echo "stack:  ${STACK}"
} | tee "${OUT}/versions.txt"

if [ "$#" -gt 0 ]; then
  PROGRAMS=("$@")
else
  PROGRAMS=()
  for d in "${CORPUS}"/*/; do
    [ -f "${d}Pulumi.yaml" ] && PROGRAMS+=("$(basename "${d}")")
  done
fi
[ "${#PROGRAMS[@]}" -gt 0 ] || die "no programs with a Pulumi.yaml under ${CORPUS}"

# refuse_nonlocal prints the endpoint lines in a stack config that point anywhere but this machine: URLs,
# and azure:metadataHost, which names a host with no scheme.
refuse_nonlocal() {
  grep -nE 'https?://' "$1" | grep -vE 'https?://(localhost|127\.0\.0\.1|\[::1\]|0\.0\.0\.0)([:/]|$)' || true
  grep -nE '^[[:space:]]*[A-Za-z0-9_-]+:(metadataHost|flociAzHost):' "$1" |
    grep -vE ':(metadataHost|flociAzHost):[[:space:]]*"?(localhost|127\.0\.0\.1):[0-9]+"?[[:space:]]*$' || true
}

# Azure (INTENTIUS/plunge#16). pulumi-azure is azurerm: with azure:metadataHost set, it reads every endpoint
# from https://<host>/metadata/endpoints, so floci-az's TLS has to verify. The real AZURE_* and ARM_* values
# were unset above; what is exported here is floci-az's fakes, the same ones the stack configs carry, so a
# provider that reads the environment ahead of its config still signs in to floci-az and nowhere else.
# AZURE_CONFIG_DIR names an empty directory, so an Azure CLI started by anything finds no login.
azure_setup() {
  local host="${FLOCI_AZ_HOST:-localhost:4577}"
  case "${host}" in
    localhost:[0-9]*|127.0.0.1:[0-9]*) ;;
    *) die "FLOCI_AZ_HOST=${host} is not a loopback host:port, so it is not floci-az" ;;
  esac
  local ca="${FLOCI_AZ_CA_FILE:-}"
  if [ -z "${ca}" ]; then
    ca="${OUT}/floci-az-ca.pem"
    # floci-az serves HTTP and HTTPS on one port; the certificate is fetched over plain HTTP.
    curl -sf "http://${host}/_floci/tls-cert" -o "${ca}" ||
      die "could not fetch floci-az's certificate from http://${host}/_floci/tls-cert (is floci-az up? set FLOCI_AZ_HOST or FLOCI_AZ_CA_FILE)"
  fi
  [ -s "${ca}" ] || die "floci-az CA file ${ca} is empty or missing"
  if [ "$(uname -s)" = "Darwin" ]; then
    >&2 echo "plunge-baseline: macOS: Go ignores SSL_CERT_FILE and trusts only the keychain, so floci-az's"
    >&2 echo "  certificate must be trusted there (choudoufu's scripts/floci-az-mac-cert.sh makes one to mount)"
  fi
  export SSL_CERT_FILE="${ca}"
  mkdir -p "${OUT}/.azure-no-login"
  export AZURE_CONFIG_DIR="${OUT}/.azure-no-login"
  export ARM_SUBSCRIPTION_ID=00000000-0000-0000-0000-00000000c0de
  export ARM_TENANT_ID=00000000-0000-0000-0000-0000000071e7
  export ARM_CLIENT_ID=00000000-0000-0000-0000-00000000c11e
  export ARM_CLIENT_SECRET=floci-az-does-not-check-this
  export ARM_METADATA_HOSTNAME="${host}" ARM_METADATA_HOST="${host}"
  export ARM_USE_CLI=false ARM_USE_MSI=false ARM_USE_OIDC=false ARM_USE_AKS_WORKLOAD_IDENTITY=false
  # azurerm refuses ARM_SKIP_PROVIDER_REGISTRATION beside resource_provider_registrations, which the stack
  # config already sets; the deprecated variable is left unset.
  export ARM_RESOURCE_PROVIDER_REGISTRATIONS=none
  export ARM_DISABLE_TERRAFORM_PARTNER_ID=true
  echo "azure:  floci-az at ${host}, CA ${ca}" | tee -a "${OUT}/versions.txt"
}

for prog in "${PROGRAMS[@]}"; do
  if grep -qE '^[[:space:]]*azure:metadataHost:' "${CORPUS}/${prog}/Pulumi.${STACK}.yaml" 2>/dev/null; then
    azure_setup
    break
  fi
done

verdict() {
  echo "VERDICT $1 $2" | tee -a "${VERDICTS}"
}

# run_engine <engine-name> <binary> <program> <outdir>
run_engine() {
  local name="$1" bin="$2" prog="$3" dir="$4"
  local work="${OUT}/.work/${name}/${prog}"
  local state="${OUT}/.state/${name}/${prog}"
  mkdir -p "${work}" "${state}" || return 1
  # A private copy of the program, without dependency or build output.
  (cd "${CORPUS}/${prog}" && tar -cf - --exclude node_modules --exclude bin --exclude obj \
    --exclude venv --exclude .venv --exclude __pycache__ .) | (cd "${work}" && tar -xf -) || return 1

  (
    cd "${work}"
    export PATH="$(dirname "${bin}"):${PATH}"
    export PULUMI_BACKEND_URL="file://${state}"
    # Chained with && because errexit is off inside a function called from an `if`.
    "${bin}" install >>"${dir}/${name}.stderr" 2>&1 &&
      "${bin}" stack init "${STACK}" --secrets-provider passphrase --non-interactive \
        >>"${dir}/${name}.stderr" 2>&1 &&
      "${bin}" preview --stack "${STACK}" --json --non-interactive \
        >"${dir}/${name}.preview.json" 2>>"${dir}/${name}.stderr"
  )
}

for prog in "${PROGRAMS[@]}"; do
  dir="${OUT}/${prog}"
  mkdir -p "${dir}"
  cfg="${CORPUS}/${prog}/Pulumi.${STACK}.yaml"
  if [ ! -f "${CORPUS}/${prog}/Pulumi.yaml" ] || [ ! -f "${cfg}" ]; then
    echo "missing Pulumi.yaml or Pulumi.${STACK}.yaml" > "${dir}/compare.txt"
    verdict "${prog}" ERROR
    continue
  fi
  nonlocal="$(refuse_nonlocal "${cfg}")"
  if [ -n "${nonlocal}" ]; then
    { echo "refused: Pulumi.${STACK}.yaml points outside this machine:"; echo "${nonlocal}"; } > "${dir}/compare.txt"
    verdict "${prog}" ERROR
    continue
  fi

  failed=""
  for engine in pulumi plunge; do
    if [ "${engine}" = plunge ]; then bin="${PLUNGE}"; else bin="${PULUMI}"; fi
    if ! run_engine "${engine}" "${bin}" "${prog}" "${dir}"; then
      failed="${failed} ${engine}"
    fi
  done
  if [ -n "${failed}" ]; then
    echo "engine failed:${failed} (see <engine>.stderr)" > "${dir}/compare.txt"
    verdict "${prog}" ERROR
    continue
  fi

  v="$(python3 -I - "${dir}" <<'PY'
import json, sys, pathlib

d = pathlib.Path(sys.argv[1])

def normalize(engine):
    raw = json.loads((d / f"{engine}.preview.json").read_text())
    steps = []
    for s in raw.get("steps") or []:
        st = s.get("newState") or s.get("oldState") or {}
        steps.append({
            "op": s.get("op"),
            "urn": s.get("urn"),
            "type": st.get("type"),
            "inputs": st.get("inputs") or {},
        })
    steps.sort(key=lambda s: (s["urn"] or "", s["op"] or ""))
    norm = {"steps": steps, "changeSummary": raw.get("changeSummary") or {}}
    (d / f"{engine}.normalized.json").write_text(json.dumps(norm, indent=2, sort_keys=True) + "\n")
    return norm

a, b = normalize("pulumi"), normalize("plunge")
lines = []
shape = lambda n: [(s["op"], s["urn"], s["type"]) for s in n["steps"]]
if shape(a) != shape(b) or a["changeSummary"] != b["changeSummary"]:
    sa, sb = set(shape(a)), set(shape(b))
    for x in sorted(sa - sb, key=str):
        lines.append(f"only in pulumi: {x[0]} {x[1]}")
    for x in sorted(sb - sa, key=str):
        lines.append(f"only in plunge: {x[0]} {x[1]}")
    if a["changeSummary"] != b["changeSummary"]:
        lines.append(f"changeSummary: pulumi {a['changeSummary']} plunge {b['changeSummary']}")
    verdict = "DIFF-STEPS"
else:
    for x, y in zip(a["steps"], b["steps"]):
        if x["inputs"] != y["inputs"]:
            keys = sorted(k for k in set(x["inputs"]) | set(y["inputs"])
                          if x["inputs"].get(k) != y["inputs"].get(k))
            lines.append(f"inputs differ: {x['urn']}: {', '.join(keys)}")
    verdict = "DIFF-INPUTS" if lines else "MATCH"
lines.insert(0, f"{verdict}: {len(a['steps'])} steps (pulumi), {len(b['steps'])} steps (plunge)")
(d / "compare.txt").write_text("\n".join(lines) + "\n")
print(verdict)
PY
)" || v=ERROR
  verdict "${prog}" "${v}"
done

echo "results in ${OUT}"
if grep -qE ' (DIFF-STEPS|ERROR)$' "${VERDICTS}"; then
  exit 1
fi
