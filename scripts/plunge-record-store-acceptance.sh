#!/usr/bin/env bash
#
# The maintainer's acceptance run of the plunge record backend on a real record store (INTENTIUS/plunge#6, P-L3).
#
# The maintainer runs this by hand; nothing in CI calls it. By default it talks to floci's S3 on localhost
# only. A store that is not on loopback is refused unless PLUNGE_ACCEPTANCE_REAL_STORE=yes.
#
# Usage:
#   scripts/plunge-record-store-acceptance.sh
#   STORE_URL='plunge+gs://plunge-acceptance/run-1' STORAGE_EMULATOR_HOST=localhost:4588 \
#     scripts/plunge-record-store-acceptance.sh                       # the GCS leg, on floci-gcp
#
# Environment:
#   PLUNGE      plunge binary                                    (default: bin/plunge)
#   STORE_URL   the plunge backend URL                           (default: floci S3, see below)
#   S3_ENDPOINT floci's S3 endpoint, for the default URL and the listing (default: http://localhost:4566)
#   BUCKET      the bucket, for the default URL                  (default: plunge-acceptance)
#   OUT         output directory                                 (default: plunge-acceptance-out/<UTC timestamp>)
#   PULUMI_CONFIG_PASSPHRASE                                     (default: plunge-acceptance)
#   STORAGE_EMULATOR_HOST  floci-gcp as host:port, for a plunge+gs:// STORE_URL (loopback only; the script then
#                          sets GOOGLE_OAUTH_ACCESS_TOKEN to a fake static token, which plunge requires against an
#                          emulator since INTENTIUS/plunge#50 and floci-gcp accepts)
#   GCS_PROJECT            the project a new GCS bucket is created in        (default: plc-corpus)
#   PLUNGE_ACCEPTANCE_REAL_STORE=yes  allow a store that is not on loopback (a real bucket; it costs money)
#
# The default STORE_URL is
#   plunge+s3://$BUCKET/<run id>?endpoint=$S3_ENDPOINT&s3ForcePathStyle=true&region=us-east-1
# with a fresh prefix per run, so runs never share records. The bucket is created with the aws CLI when it is
# missing and the aws CLI is on PATH. A plunge+file:// STORE_URL works too, for a dry rehearsal of the script.
#
# Opening a plunge+s3:// or plunge+gs:// store checks the bucket's settings and refuses one that fails them
# (INTENTIUS/plunge#20), so the script sets them and the run needs no allow_insecure / PLUNGE_ALLOW_INSECURE
# waiver (INTENTIUS/plunge#34):
#   S3   versioning Enabled; an enabled lifecycle rule with no filter expiring noncurrent versions after 30 days;
#        all four public access block settings on
#   GCS  Object Versioning; a Delete rule for versions 30 days after they become noncurrent (it never matches a
#        live object); public access prevention enforced; uniform bucket-level access
# On loopback it sets them on every run, on a bucket it created or found. On a real store it sets them only on a
# bucket it created itself, and leaves an existing real bucket's settings to its owner (an open then says which
# setting fails). A plunge+gs:// store on loopback is one whose STORAGE_EMULATOR_HOST is loopback; the bucket is
# created and configured through floci-gcp's JSON API. A real GCS bucket is created with gcloud when it is
# missing and gcloud is on PATH.
#
# The program is a Pulumi YAML project of pulumi-random RandomStrings (no cloud at all: the record store is
# the only thing under test). Its plugin is downloaded into $OUT/home on first use.
#
# What it does, each step's output under $OUT:
#   1. base/        one stack, resX and resY at keeper "1"
#   2. a/, b/       two concurrent `plunge up` runs from copies of the program: a/ sets resX's keeper to 2,
#                   b/ sets resY's to 2. Both start from the same records.
#   3. export.json  `plunge stack export` after both; stack.txt is `plunge stack --show-urns`, which assembles
#                   the snapshot and fails on an integrity error
#      listing.txt  the store's resource record keys, each decoded to its URN
#   4. newer/       a run stamped PLUNGE_PROGRAM_COMMIT=newer-commit (now) that adds resZ
#      older/       a run stamped PLUNGE_PROGRAM_COMMIT=older-commit (a day earlier) whose program has no resZ
#      export-after-older.json
#
# and $OUT/verdicts.txt:
#   VERDICT concurrent-disjoint PASS|FAIL   both runs exited 0, the export holds resX=2 and resY=2, and
#                                           `plunge stack` assembled the snapshot (integrity passed)
#   VERDICT one-record-per-urn  PASS|FAIL|SKIPPED   the listing has exactly one resource record for each URN
#                                           in the export (SKIPPED for a store this script cannot list)
#   VERDICT older-run-keeps     PASS|FAIL   the older run exited 0, its output has the delete-guard warning
#                                           naming resZ and both stamps, and resZ is still in the export
#
# Read the verdict lines and the recorded files, not the exit code. The exit code is 0 only when no verdict
# is FAIL.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PLUNGE="${PLUNGE:-${ROOT}/bin/plunge}"
S3_ENDPOINT="${S3_ENDPOINT:-http://localhost:4566}"
BUCKET="${BUCKET:-plunge-acceptance}"
RUN_ID="run-$(date -u +%Y%m%dT%H%M%SZ)"
OUT="${OUT:-${ROOT}/plunge-acceptance-out/${RUN_ID}}"
STORE_URL="${STORE_URL:-plunge+s3://${BUCKET}/${RUN_ID}?endpoint=${S3_ENDPOINT}&s3ForcePathStyle=true&region=us-east-1}"
export PULUMI_CONFIG_PASSPHRASE="${PULUMI_CONFIG_PASSPHRASE:-plunge-acceptance}"

PROJECT=plunge-record-store
STACK=acceptance

die() {
  >&2 echo "plunge-record-store-acceptance: $*"
  exit 2
}

[ -x "${PLUNGE}" ] || die "plunge binary not found at ${PLUNGE} (run 'make plunge' or set PLUNGE)"
command -v python3 >/dev/null || die "python3 is required"
PLUNGE="$(cd "$(dirname "${PLUNGE}")" && pwd)/$(basename "${PLUNGE}")"

case "${STORE_URL}" in
  plunge+*) ;;
  *) die "STORE_URL must be a plunge backend URL (plunge+s3://, plunge+file://, ...): ${STORE_URL}" ;;
esac

# The store must be on loopback unless the maintainer says otherwise.
store_is_local() {
  case "${STORE_URL}" in
    plunge+file://*) return 0 ;;
    plunge+gs://*)
      # The GCS client and the DIY backend's gcsblob both go to STORAGE_EMULATOR_HOST when it is set, and to
      # storage.googleapis.com when it is not.
      case "${STORAGE_EMULATOR_HOST:-}" in
        localhost:[0-9]*|127.0.0.1:[0-9]*) return 0 ;;
        *) return 1 ;;
      esac
      ;;
  esac
  python3 - "${STORE_URL}" <<'EOF'
import sys, urllib.parse
u = urllib.parse.urlparse(sys.argv[1][len("plunge+"):])
q = urllib.parse.parse_qs(u.query)
endpoint = q.get("endpoint", [""])[0]
host = urllib.parse.urlparse(endpoint).hostname if endpoint else None
sys.exit(0 if host in ("localhost", "127.0.0.1", "::1") else 1)
EOF
}
if ! store_is_local && [ "${PLUNGE_ACCEPTANCE_REAL_STORE:-}" != "yes" ]; then
  die "the store in ${STORE_URL} is not on loopback; set PLUNGE_ACCEPTANCE_REAL_STORE=yes to run against it"
fi

# No real cloud credentials reach plunge unless a real store was asked for. floci accepts any key.
if [ "${PLUNGE_ACCEPTANCE_REAL_STORE:-}" != "yes" ]; then
  for v in $(env | sed -n 's/^\(AWS_[A-Z_]*\)=.*/\1/p;s/^\(AZURE_[A-Z_]*\)=.*/\1/p;s/^\(GOOGLE_[A-Z_]*\)=.*/\1/p'); do
    unset "${v}"
  done
  export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
  export AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_CONFIG_FILE=/dev/null AWS_EC2_METADATA_DISABLED=true
  # Against STORAGE_EMULATOR_HOST, plunge refuses to open a plunge+gs:// store without GOOGLE_OAUTH_ACCESS_TOKEN or
  # GOOGLE_CREDENTIALS, so the metadata store never falls back to ADC (INTENTIUS/plunge#50). floci-gcp takes any
  # token. Only the loopback emulator leg gets it; a real store keeps the maintainer's own credentials.
  case "${STORE_URL}" in
    plunge+gs://*) store_is_local && export GOOGLE_OAUTH_ACCESS_TOKEN=floci-gcp-fake ;;
  esac
fi
export PULUMI_SKIP_UPDATE_CHECK=true PULUMI_SKIP_CONFIRMATIONS=true
unset PULUMI_ACCESS_TOKEN
export PULUMI_BACKEND_URL="${STORE_URL}"

mkdir -p "${OUT}"
export PULUMI_HOME="${OUT}/home"
if [ "${PLUNGE_ACCEPTANCE_REAL_STORE:-}" != "yes" ]; then
  # No application-default credentials in reach either: gcloud's configuration is an empty directory.
  mkdir -p "${OUT}/gcloud"
  export CLOUDSDK_CONFIG="${OUT}/gcloud"
fi
VERDICTS="${OUT}/verdicts.txt"
: > "${VERDICTS}"
echo "${STORE_URL}" > "${OUT}/store-url.txt"

verdict() {
  echo "VERDICT $1 $2" | tee -a "${VERDICTS}"
}

# The S3 bucket and key prefix of the store, when it is S3.
s3_parts() {
  python3 - "${STORE_URL}" <<'EOF'
import sys, urllib.parse
u = urllib.parse.urlparse(sys.argv[1][len("plunge+"):])
if u.scheme != "s3":
    sys.exit(1)
q = urllib.parse.parse_qs(u.query)
print(u.netloc, u.path.strip("/"), q.get("endpoint", [""])[0])
EOF
}

# The GCS bucket and object prefix of the store, when it is GCS.
gs_parts() {
  python3 - "${STORE_URL}" <<'EOF'
import sys, urllib.parse
u = urllib.parse.urlparse(sys.argv[1][len("plunge+"):])
if u.scheme != "gs":
    sys.exit(1)
print(u.netloc, u.path.strip("/"))
EOF
}

S3_BUCKET="" S3_PREFIX="" S3_EP="" GS_BUCKET="" GS_PREFIX=""
if parts="$(s3_parts)"; then
  read -r S3_BUCKET S3_PREFIX S3_EP <<< "${parts}"
  if command -v aws >/dev/null; then
    ep_args=()
    [ -n "${S3_EP}" ] && ep_args=(--endpoint-url "${S3_EP}")
    created=no
    if ! aws "${ep_args[@]}" s3api head-bucket --bucket "${S3_BUCKET}" >/dev/null 2>&1; then
      aws "${ep_args[@]}" s3api create-bucket --bucket "${S3_BUCKET}" >/dev/null
      created=yes
    fi
    if store_is_local || [ "${created}" = yes ]; then
      aws "${ep_args[@]}" s3api put-bucket-versioning --bucket "${S3_BUCKET}" \
        --versioning-configuration Status=Enabled
      aws "${ep_args[@]}" s3api put-bucket-lifecycle-configuration --bucket "${S3_BUCKET}" --lifecycle-configuration \
        '{"Rules":[{"ID":"records-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}'
      aws "${ep_args[@]}" s3api put-public-access-block --bucket "${S3_BUCKET}" --public-access-block-configuration \
        BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
      echo "s3 bucket ${S3_BUCKET}: versioning, lifecycle and public access block set" >> "${OUT}/bucket-setup.txt"
    fi
  fi
elif parts="$(gs_parts)"; then
  read -r GS_BUCKET GS_PREFIX <<< "${parts}"
  if store_is_local; then
    # floci-gcp's JSON API: create the bucket with its settings, or patch them onto one that is there.
    python3 -I - "${STORAGE_EMULATOR_HOST}" "${GS_BUCKET}" "${GCS_PROJECT:-plc-corpus}" "${OUT}/bucket-setup.txt" <<'EOF'
import json, sys, urllib.error, urllib.parse, urllib.request
host, bucket, project, log = sys.argv[1:5]
base = f"http://{host}/storage/v1/b"
settings = {
    "versioning": {"enabled": True},
    "lifecycle": {"rule": [{"action": {"type": "Delete"}, "condition": {"daysSinceNoncurrentTime": 30}}]},
    "iamConfiguration": {"uniformBucketLevelAccess": {"enabled": True}, "publicAccessPrevention": "enforced"},
}
def call(method, url, body):
    req = urllib.request.Request(url, data=json.dumps(body).encode(), method=method,
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as r:
        return json.load(r)
try:
    call("POST", f"{base}?project={urllib.parse.quote(project)}", dict(settings, name=bucket))
    how = "created"
except urllib.error.HTTPError as e:
    if e.code != 409:
        sys.exit(f"creating gs://{bucket} on {host}: HTTP {e.code}: {e.read().decode(errors='replace')}")
    call("PATCH", f"{base}/{urllib.parse.quote(bucket, safe='')}", settings)
    how = "patched"
with open(log, "a") as f:
    f.write(f"gs bucket {bucket} {how} on {host}: versioning, noncurrent lifecycle, public access prevention "
            "enforced, uniform bucket-level access\n")
EOF
  elif command -v gcloud >/dev/null && ! gcloud storage buckets describe "gs://${GS_BUCKET}" >/dev/null 2>&1; then
    lifecycle="${OUT}/gcs-lifecycle.json"
    echo '{"rule":[{"action":{"type":"Delete"},"condition":{"daysSinceNoncurrentTime":30}}]}' > "${lifecycle}"
    gcloud storage buckets create "gs://${GS_BUCKET}" ${GCS_PROJECT:+--project "${GCS_PROJECT}"} \
      --uniform-bucket-level-access --public-access-prevention
    gcloud storage buckets update "gs://${GS_BUCKET}" --versioning --lifecycle-file "${lifecycle}"
    echo "gs bucket ${GS_BUCKET} created with gcloud and its settings set" >> "${OUT}/bucket-setup.txt"
  fi
fi

# write_program DIR WITH_Z writes the project into DIR: resX and resY, keepers from config, and resZ when WITH_Z.
write_program() {
  local dir="$1" with_z="$2"
  mkdir -p "${dir}"
  {
    cat <<EOF
name: ${PROJECT}
runtime: yaml
config:
  x:
    type: string
    default: "1"
  y:
    type: string
    default: "1"
resources:
  resX:
    type: random:RandomString
    properties:
      length: 12
      special: false
      keepers:
        v: \${x}
  resY:
    type: random:RandomString
    properties:
      length: 12
      special: false
      keepers:
        v: \${y}
EOF
    if [ "${with_z}" = "yes" ]; then
      cat <<EOF
  resZ:
    type: random:RandomString
    properties:
      length: 12
      special: false
EOF
    fi
  } > "${dir}/Pulumi.yaml"
}

# copy_workspace FROM TO copies the program and its stack config (which holds the passphrase salt).
copy_workspace() {
  mkdir -p "$2"
  cp "$1/Pulumi.yaml" "$1/Pulumi.${STACK}.yaml" "$2/"
}

run_up() {
  local dir="$1" name="$2"
  shift 2
  (cd "${dir}" && env "$@" "${PLUNGE}" up --yes --skip-preview -s "${STACK}" > "${OUT}/${name}.up.txt" 2>&1)
}

# 1. Base.
write_program "${OUT}/base" no
(cd "${OUT}/base" && "${PLUNGE}" stack init "${STACK}" > "${OUT}/base.init.txt" 2>&1)
run_up "${OUT}/base" base PLUNGE_PROGRAM_COMMIT=base-commit PLUNGE_PROGRAM_TIME="$(date -u +%s)" ||
  die "the base run failed; see ${OUT}/base.up.txt"

# 2. Two concurrent runs on disjoint resources, from the same base.
copy_workspace "${OUT}/base" "${OUT}/a"
copy_workspace "${OUT}/base" "${OUT}/b"
(cd "${OUT}/a" && "${PLUNGE}" config set x 2 -s "${STACK}")
(cd "${OUT}/b" && "${PLUNGE}" config set y 2 -s "${STACK}")
gen_time="$(date -u +%s)"
set +e
run_up "${OUT}/a" a PLUNGE_PROGRAM_COMMIT=base-commit PLUNGE_PROGRAM_TIME="${gen_time}" &
pid_a=$!
run_up "${OUT}/b" b PLUNGE_PROGRAM_COMMIT=base-commit PLUNGE_PROGRAM_TIME="${gen_time}" &
pid_b=$!
wait "${pid_a}"
rc_a=$?
wait "${pid_b}"
rc_b=$?
set -e
echo "a=${rc_a} b=${rc_b}" > "${OUT}/concurrent.exit.txt"

# 3. The result: export, assembly with integrity, store listing.
(cd "${OUT}/base" && "${PLUNGE}" stack export -s "${STACK}" --file "${OUT}/export.json")
set +e
(cd "${OUT}/base" && "${PLUNGE}" stack -s "${STACK}" --show-urns > "${OUT}/stack.txt" 2>&1)
rc_stack=$?
set -e

if [ "${rc_a}" = 0 ] && [ "${rc_b}" = 0 ] && [ "${rc_stack}" = 0 ] && python3 - "${OUT}/export.json" <<'EOF'
import json, sys
d = json.load(open(sys.argv[1]))["deployment"]
got = {}
for r in d.get("resources", []):
    if r.get("delete"):
        continue
    name = r["urn"].rsplit("::", 1)[-1]
    got[name] = (r.get("inputs") or {}).get("keepers", {}).get("v")
ok = got.get("resX") == "2" and got.get("resY") == "2"
print("keepers:", got)
sys.exit(0 if ok else 1)
EOF
then
  verdict concurrent-disjoint PASS
else
  verdict concurrent-disjoint FAIL
fi

# The resource record keys under the stack's namespace, decoded.
list_resource_records() {
  local suffix=".pulumi/records/stacks/${PROJECT}/${STACK}/resources/"
  case "${STORE_URL}" in
    plunge+file://*)
      local dir="${STORE_URL#plunge+file://}"
      dir="${dir%%\?*}"
      [ -d "${dir}/${suffix}" ] || return 1
      # A record's name is base64url, which has no "."; temp and lock files do.
      local f
      for f in "${dir}/${suffix}"*; do
        f="$(basename "${f}")"
        case "${f}" in *.*) continue ;; esac
        echo "${suffix}${f}"
      done
      ;;
    plunge+s3://*)
      command -v aws >/dev/null || return 1
      local prefix="${suffix}"
      [ -n "${S3_PREFIX}" ] && prefix="${S3_PREFIX}/${suffix}"
      local ep_args=()
      [ -n "${S3_EP}" ] && ep_args=(--endpoint-url "${S3_EP}")
      aws "${ep_args[@]}" s3api list-objects-v2 --bucket "${S3_BUCKET}" --prefix "${prefix}" \
        --query 'Contents[].Key' --output text | tr '\t' '\n' | grep -v '^None$'
      ;;
    plunge+gs://*)
      # floci-gcp only: a real bucket's listing needs credentials this script does not hold.
      store_is_local || return 1
      local prefix="${suffix}"
      [ -n "${GS_PREFIX}" ] && prefix="${GS_PREFIX}/${suffix}"
      python3 -I - "${STORAGE_EMULATOR_HOST}" "${GS_BUCKET}" "${prefix}" <<'EOF'
import json, sys, urllib.parse, urllib.request
host, bucket, prefix = sys.argv[1:4]
token = ""
while True:
    q = {"prefix": prefix, "fields": "items(name),nextPageToken"}
    if token:
        q["pageToken"] = token
    url = f"http://{host}/storage/v1/b/{urllib.parse.quote(bucket, safe='')}/o?{urllib.parse.urlencode(q)}"
    with urllib.request.urlopen(url) as r:
        page = json.load(r)
    for o in page.get("items") or []:
        name = o["name"]
        if "." not in name.rsplit("/", 1)[-1]:
            print(name)
    token = page.get("nextPageToken") or ""
    if not token:
        break
EOF
      ;;
    *) return 1 ;;
  esac
}

if keys="$(list_resource_records)"; then
  echo "${keys}" > "${OUT}/listing.keys.txt"
  if python3 - "${OUT}/listing.keys.txt" "${OUT}/export.json" "${OUT}/listing.txt" <<'EOF'
import base64, collections, json, sys
keys = [k.strip() for k in open(sys.argv[1]) if k.strip()]
urns = []
with open(sys.argv[3], "w") as out:
    for k in keys:
        seg = k.rsplit("/", 1)[-1]
        urn = base64.urlsafe_b64decode(seg + "=" * (-len(seg) % 4)).decode()
        urns.append(urn)
        out.write(f"{k}\t{urn}\n")
d = json.load(open(sys.argv[2]))["deployment"]
want = sorted({r["urn"] for r in d.get("resources", [])})
count = collections.Counter(urns)
dupes = [u for u, n in count.items() if n > 1]
ok = not dupes and sorted(count) == want
print("records:", len(urns), "urns in export:", len(want), "duplicates:", dupes)
sys.exit(0 if ok else 1)
EOF
  then
    verdict one-record-per-urn PASS
  else
    verdict one-record-per-urn FAIL
  fi
else
  verdict one-record-per-urn SKIPPED
fi

# 4. An older program does not delete what a newer one created.
now="$(date -u +%s)"
write_program "${OUT}/newer" yes
cp "${OUT}/base/Pulumi.${STACK}.yaml" "${OUT}/newer/"
run_up "${OUT}/newer" newer PLUNGE_PROGRAM_COMMIT=newer-commit PLUNGE_PROGRAM_TIME="${now}" ||
  die "the newer run failed; see ${OUT}/newer.up.txt"

write_program "${OUT}/older" no
cp "${OUT}/base/Pulumi.${STACK}.yaml" "${OUT}/older/"
set +e
run_up "${OUT}/older" older PLUNGE_PROGRAM_COMMIT=older-commit PLUNGE_PROGRAM_TIME="$((now - 86400))"
rc_older=$?
set -e
(cd "${OUT}/base" && "${PLUNGE}" stack export -s "${STACK}" --file "${OUT}/export-after-older.json")

if [ "${rc_older}" = 0 ] &&
  grep -q "resZ" "${OUT}/older.up.txt" &&
  grep -q "newer-commit (commit time ${now})" "${OUT}/older.up.txt" &&
  grep -q "older-commit (commit time $((now - 86400)))" "${OUT}/older.up.txt" &&
  python3 - "${OUT}/export-after-older.json" <<'EOF'
import json, sys
d = json.load(open(sys.argv[1]))["deployment"]
sys.exit(0 if any(r["urn"].endswith("::resZ") and not r.get("delete") for r in d.get("resources", [])) else 1)
EOF
then
  verdict older-run-keeps PASS
else
  verdict older-run-keeps FAIL
fi

echo
echo "Recorded under ${OUT}. Verdicts:"
cat "${VERDICTS}"
! grep -q ' FAIL$' "${VERDICTS}"
