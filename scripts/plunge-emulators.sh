#!/usr/bin/env bash
#
# Starts, stops and reports the three emulators the plunge corpus, its baseline and the store conformance legs
# run against (tests/plunge-corpus/README.md, "Running against the emulators").
#
# Usage:
#   scripts/plunge-emulators.sh up     [floci|floci-gcp|floci-az ...]
#   scripts/plunge-emulators.sh down   [floci|floci-gcp|floci-az ...]
#   scripts/plunge-emulators.sh status [floci|floci-gcp|floci-az ...]
#
# With no names, all three. Images come from tests/plunge-corpus/emulator-images (pinned by digest, copies of
# choudoufu's pins; scripts/check-emulator-images.sh diffs them). Every port is published on 127.0.0.1 only.
#
# Environment (defaults match the corpus stack files, which name these ports):
#   PLUNGE_FLOCI_NAME          floci's container name        (default: plunge-floci)
#   PLUNGE_FLOCI_PORT          floci's host port             (default: 4566)
#   PLUNGE_FLOCI_GCP_NAME      floci-gcp's container name    (default: plunge-floci-gcp)
#   PLUNGE_FLOCI_GCP_PORT      floci-gcp's host port         (default: 4588)
#   PLUNGE_FLOCI_GCP_PROJECT   FLOCI_GCP_DEFAULT_PROJECT_ID  (default: plc-corpus)
#   PLUNGE_FLOCI_AZ_NAME       floci-az's container name     (default: plunge-floci-az)
#   PLUNGE_FLOCI_AZ_PORT       floci-az's host port          (default: 4577); FLOCI_AZ_STORAGE_SUFFIX names it
#   FLOCI_AZ_TLS_DIR           cert.pem and key.pem floci-az serves, made by choudoufu's
#                              scripts/floci-az-mac-cert.sh   (default: ~/.config/choudoufu/floci-az-tls)
#   PLUNGE_EMULATORS_READY_SECS  how long `up` waits for each emulator to answer (default: 90)
#
# floci-az runs with TLS. With cert.pem and key.pem in FLOCI_AZ_TLS_DIR they are mounted read-only and served;
# on macOS they are required, since Go there trusts only the keychain and floci-az's own per-start CA cannot
# have been trusted in advance. On Linux, with no certificate there, floci-az generates its own CA, which
# `up` fetches into $XDG_CACHE_HOME/plunge-emulators/<name>-ca.pem and prints.
#
# Ownership: each container this script starts carries the label io.intentius.plunge-emulators=<name> and
# the pinned image. `up` reuses a running container only when it carries that label and runs the pinned
# image; `down` stops only a container that carries the label. A container of the same name that this
# script did not start (another harness's, or someone's hand `docker run`) is refused by name and left alone.
#
# `up` waits until each emulator answers its readiness URL (floci /_localstack/health, floci-gcp /health,
# floci-az /ready) and prints what to export for it. Exit status: 0 when every named emulator is up (or
# down, for `down`); 1 when one is not; 2 on a usage or setup error. `status` exits 0 only when every named
# emulator is running, labelled, on the pinned image and ready.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGES="${ROOT}/tests/plunge-corpus/emulator-images"
LABEL=io.intentius.plunge-emulators

PLUNGE_FLOCI_NAME="${PLUNGE_FLOCI_NAME:-plunge-floci}"
PLUNGE_FLOCI_PORT="${PLUNGE_FLOCI_PORT:-4566}"
PLUNGE_FLOCI_GCP_NAME="${PLUNGE_FLOCI_GCP_NAME:-plunge-floci-gcp}"
PLUNGE_FLOCI_GCP_PORT="${PLUNGE_FLOCI_GCP_PORT:-4588}"
PLUNGE_FLOCI_GCP_PROJECT="${PLUNGE_FLOCI_GCP_PROJECT:-plc-corpus}"
PLUNGE_FLOCI_AZ_NAME="${PLUNGE_FLOCI_AZ_NAME:-plunge-floci-az}"
PLUNGE_FLOCI_AZ_PORT="${PLUNGE_FLOCI_AZ_PORT:-4577}"
FLOCI_AZ_TLS_DIR="${FLOCI_AZ_TLS_DIR:-${HOME}/.config/choudoufu/floci-az-tls}"
READY_SECS="${PLUNGE_EMULATORS_READY_SECS:-90}"
CACHE="${XDG_CACHE_HOME:-${HOME}/.cache}/plunge-emulators"

die() {
  >&2 echo "plunge-emulators: $*"
  exit 2
}

usage() {
  >&2 echo "usage: scripts/plunge-emulators.sh up|down|status [floci|floci-gcp|floci-az ...]"
  exit 2
}

[ $# -ge 1 ] || usage
cmd="$1"
shift
case "${cmd}" in
  up|down|status) ;;
  *) usage ;;
esac

if [ $# -eq 0 ]; then
  set -- floci floci-gcp floci-az
fi
for e in "$@"; do
  case "${e}" in
    floci|floci-gcp|floci-az) ;;
    *) die "unknown emulator ${e} (floci, floci-gcp or floci-az)" ;;
  esac
done

for p in "${PLUNGE_FLOCI_PORT}" "${PLUNGE_FLOCI_GCP_PORT}" "${PLUNGE_FLOCI_AZ_PORT}" "${READY_SECS}"; do
  case "${p}" in
    ''|*[!0-9]*) die "${p:-(empty)} is not a number (a port or PLUNGE_EMULATORS_READY_SECS)" ;;
  esac
done

command -v docker >/dev/null || die "docker is not installed"
command -v curl >/dev/null || die "curl is required"
[ -f "${IMAGES}" ] || die "${IMAGES} is missing"

# image <emulator> prints the pinned image for it.
image() {
  local img
  img="$(awk -v n="$1" '$1 == n { print $2 }' "${IMAGES}")"
  case "${img}" in
    *@sha256:*) printf '%s\n' "${img}" ;;
    *) die "${IMAGES} pins no digest for $1 (got '${img}')" ;;
  esac
}

name_of() {
  case "$1" in
    floci) echo "${PLUNGE_FLOCI_NAME}" ;;
    floci-gcp) echo "${PLUNGE_FLOCI_GCP_NAME}" ;;
    floci-az) echo "${PLUNGE_FLOCI_AZ_NAME}" ;;
  esac
}

port_of() {
  case "$1" in
    floci) echo "${PLUNGE_FLOCI_PORT}" ;;
    floci-gcp) echo "${PLUNGE_FLOCI_GCP_PORT}" ;;
    floci-az) echo "${PLUNGE_FLOCI_AZ_PORT}" ;;
  esac
}

# The port each image listens on inside its container.
inner_port_of() {
  case "$1" in
    floci) echo 4566 ;;
    floci-gcp) echo 4588 ;;
    floci-az) echo 4577 ;;
  esac
}

ready_url() {
  local port
  port="$(port_of "$1")"
  case "$1" in
    floci) echo "http://127.0.0.1:${port}/_localstack/health" ;;
    floci-gcp) echo "http://127.0.0.1:${port}/health" ;;
    # /ready, not /_floci/health: health answers as soon as the HTTP server is up, /ready once every service
    # has started. floci-az serves plain HTTP and HTTPS on one port.
    floci-az) echo "http://127.0.0.1:${port}/ready" ;;
  esac
}

# inspect <container> <format> prints docker inspect's field, or nothing when there is no such container.
inspect() {
  docker container inspect --format "$2" "$1" 2>/dev/null || true
}

# owner <container> prints the container's ownership label value, or nothing.
owner() {
  inspect "$1" "{{ index .Config.Labels \"${LABEL}\" }}"
}

is_ready() {
  curl -fsS --max-time 2 "$(ready_url "$1")" >/dev/null 2>&1
}

wait_ready() {
  local e="$1" name deadline
  name="$(name_of "${e}")"
  deadline=$(( $(date +%s) + READY_SECS ))
  until is_ready "${e}"; do
    if [ "$(inspect "${name}" '{{.State.Running}}')" != "true" ]; then
      >&2 echo "plunge-emulators: ${name} stopped before it was ready; its last log lines:"
      docker logs --tail 30 "${name}" >&2 2>&1 || true
      return 1
    fi
    if [ "$(date +%s)" -ge "${deadline}" ]; then
      >&2 echo "plunge-emulators: ${name} did not answer $(ready_url "${e}") within ${READY_SECS}s (raise PLUNGE_EMULATORS_READY_SECS); its last log lines:"
      docker logs --tail 30 "${name}" >&2 2>&1 || true
      return 1
    fi
    sleep 1
  done
}

# The floci-az certificate pair, when FLOCI_AZ_TLS_DIR holds one.
az_cert() { [ -s "${FLOCI_AZ_TLS_DIR}/cert.pem" ] && [ -s "${FLOCI_AZ_TLS_DIR}/key.pem" ]; }

# hints <emulator> prints what a run against it exports.
hints() {
  local e="$1" port
  port="$(port_of "${e}")"
  case "${e}" in
    floci)
      echo "  endpoint http://localhost:${port} (aws-* stacks; PLUNGE_CONFORMANCE_S3_URL's endpoint=)"
      ;;
    floci-gcp)
      echo "  endpoint http://localhost:${port}, project ${PLUNGE_FLOCI_GCP_PROJECT} (gcp-yaml; STORAGE_EMULATOR_HOST=localhost:${port})"
      ;;
    floci-az)
      local ca
      if az_cert; then
        ca="${FLOCI_AZ_TLS_DIR}/cert.pem"
      else
        ca="${CACHE}/$(name_of "${e}")-ca.pem"
      fi
      echo "  metadata host localhost:${port} (azure-yaml)"
      echo "  export FLOCI_AZ_HOST=localhost:${port} FLOCI_AZ_CA_FILE=${ca}"
      ;;
  esac
}

up_one() {
  local e="$1" name port img inner have_owner running have_image
  name="$(name_of "${e}")"
  port="$(port_of "${e}")"
  img="$(image "${e}")"
  inner="$(inner_port_of "${e}")"

  if [ -n "$(inspect "${name}" '{{.Id}}')" ]; then
    have_owner="$(owner "${name}")"
    if [ "${have_owner}" != "${e}" ]; then
      >&2 echo "plunge-emulators: a container named ${name} exists and this script did not start it (no ${LABEL}=${e} label); refusing to reuse it. Pick another name (PLUNGE_*_NAME) or stop it yourself."
      return 1
    fi
    running="$(inspect "${name}" '{{.State.Running}}')"
    have_image="$(inspect "${name}" '{{.Config.Image}}')"
    if [ "${running}" != "true" ]; then
      >&2 echo "plunge-emulators: ${name} exists but is not running; run 'scripts/plunge-emulators.sh down ${e}' first"
      return 1
    fi
    if [ "${have_image}" != "${img}" ]; then
      >&2 echo "plunge-emulators: ${name} runs ${have_image}, not the pinned ${img}; run 'scripts/plunge-emulators.sh down ${e}' and start it again"
      return 1
    fi
    if [ "$(docker port "${name}" "${inner}" 2>/dev/null | sed -n 's/.*:\([0-9][0-9]*\)$/\1/p' | head -1)" != "${port}" ]; then
      >&2 echo "plunge-emulators: ${name} is not published on 127.0.0.1:${port}; run 'scripts/plunge-emulators.sh down ${e}' and start it again"
      return 1
    fi
    echo "${e}: ${name} already up (started by this script), on the pinned image"
  else
    local -a args=(run -d --rm --name "${name}" --label "${LABEL}=${e}" --label "${LABEL}.image=${img}"
      -p "127.0.0.1:${port}:${inner}")
    case "${e}" in
      floci-gcp)
        args+=(-e "FLOCI_GCP_DEFAULT_PROJECT_ID=${PLUNGE_FLOCI_GCP_PROJECT}")
        ;;
      floci-az)
        args+=(-e FLOCI_AZ_TLS_ENABLED=true -e FLOCI_AZ_STORAGE_MODE=memory -e FLOCI_AZ_PARITY=azure
          -e "FLOCI_AZ_STORAGE_SUFFIX=localhost:${port}")
        if az_cert; then
          # Single-file read-only mounts: the container runs as uid 1001, and choudoufu's script makes key.pem
          # world-readable inside a 0700 directory for that reason.
          args+=(-v "${FLOCI_AZ_TLS_DIR}/cert.pem:/etc/floci-az-tls/cert.pem:ro"
            -v "${FLOCI_AZ_TLS_DIR}/key.pem:/etc/floci-az-tls/key.pem:ro"
            -e FLOCI_AZ_TLS_CERT_PATH=/etc/floci-az-tls/cert.pem
            -e FLOCI_AZ_TLS_KEY_PATH=/etc/floci-az-tls/key.pem)
        elif [ "$(uname -s)" = "Darwin" ]; then
          >&2 echo "plunge-emulators: macOS: no floci-az certificate at ${FLOCI_AZ_TLS_DIR}/cert.pem and key.pem."
          >&2 echo "  Go on macOS trusts only the keychain, so floci-az's per-start CA would fail every Azure call."
          >&2 echo "  Run choudoufu's scripts/floci-az-mac-cert.sh once, trust what it prints, or set FLOCI_AZ_TLS_DIR."
          return 1
        fi
        ;;
    esac
    args+=("${img}")
    docker "${args[@]}" >/dev/null || {
      >&2 echo "plunge-emulators: docker run for ${name} failed (is port ${port} taken?)"
      return 1
    }
    echo "${e}: started ${name} on 127.0.0.1:${port} from ${img}"
  fi

  wait_ready "${e}" || return 1
  if [ "${e}" = floci-az ] && ! az_cert; then
    mkdir -p "${CACHE}"
    local ca="${CACHE}/${name}-ca.pem"
    if ! { curl -fsS --max-time 10 "http://127.0.0.1:${port}/_floci/tls-cert" -o "${ca}" &&
      grep -q "BEGIN CERTIFICATE" "${ca}"; }; then
      >&2 echo "plunge-emulators: could not fetch a PEM CA from http://127.0.0.1:${port}/_floci/tls-cert"
      return 1
    fi
  fi
  echo "${e}: ready at $(ready_url "${e}")"
  hints "${e}"
}

down_one() {
  local e="$1" name have_owner
  name="$(name_of "${e}")"
  if [ -z "$(inspect "${name}" '{{.Id}}')" ]; then
    echo "${e}: no container named ${name}"
    return 0
  fi
  have_owner="$(owner "${name}")"
  if [ "${have_owner}" != "${e}" ]; then
    >&2 echo "plunge-emulators: ${name} was not started by this script (no ${LABEL}=${e} label); refusing to stop it"
    return 1
  fi
  docker stop "${name}" >/dev/null
  # Started with --rm, so a stop removes it; this covers one whose removal lagged or that was started otherwise.
  docker rm -f "${name}" >/dev/null 2>&1 || true
  echo "${e}: stopped ${name}"
}

status_one() {
  local e="$1" name img have_owner running have_image problems=""
  name="$(name_of "${e}")"
  img="$(image "${e}")"
  if [ -z "$(inspect "${name}" '{{.Id}}')" ]; then
    echo "${e}: DOWN (no container named ${name})"
    return 1
  fi
  have_owner="$(owner "${name}")"
  running="$(inspect "${name}" '{{.State.Running}}')"
  have_image="$(inspect "${name}" '{{.Config.Image}}')"
  [ "${have_owner}" = "${e}" ] || problems="${problems} not-started-by-this-script"
  [ "${running}" = "true" ] || problems="${problems} not-running"
  [ "${have_image}" = "${img}" ] || problems="${problems} image=${have_image}"
  is_ready "${e}" || problems="${problems} not-ready"
  if [ -n "${problems}" ]; then
    echo "${e}: NOT-OK ${name}:${problems}"
    return 1
  fi
  echo "${e}: UP ${name} on 127.0.0.1:$(port_of "${e}"), pinned image, ready"
  hints "${e}"
}

rc=0
for e in "$@"; do
  case "${cmd}" in
    up) up_one "${e}" || rc=1 ;;
    down) down_one "${e}" || rc=1 ;;
    status) status_one "${e}" || rc=1 ;;
  esac
done
exit "${rc}"
