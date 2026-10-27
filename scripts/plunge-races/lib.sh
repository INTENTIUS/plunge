#!/usr/bin/env bash
#
# Shared helpers for the plunge race scripts (INTENTIUS/plunge#7). Sourced, never run. The maintainer runs the
# scripts by hand against a local floci; nothing here talks to AWS or Pulumi Cloud.
#
# Environment:
#   PLUNGE   plunge binary           (default: bin/plunge)
#   PULUMI   stock pulumi binary     (default: pulumi on PATH; adopt-and-back-out.sh only)
#   FLOCI    floci endpoint          (default: http://localhost:4566; loopback only)
#   OUT      output directory        (default: plunge-races-out/<UTC timestamp>)
#
# Each race gets its own S3 bucket on floci as its record store (plunge+s3://), its own resource name prefix and
# its own stack, so races never see each other's objects. Two "checkouts" of one program are two directories with
# the same Pulumi.yaml and stack file against the same backend, which is two operators running at once.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PLUNGE="${PLUNGE:-${ROOT}/bin/plunge}"
FLOCI="${FLOCI:-http://localhost:4566}"
OUT="${OUT:-${ROOT}/plunge-races-out/$(date -u +%Y%m%dT%H%M%SZ)}"
STACK=race
export PULUMI_CONFIG_PASSPHRASE="${PULUMI_CONFIG_PASSPHRASE:-plunge-races}"

die() {
  >&2 echo "plunge-races: $*"
  exit 2
}

case "${FLOCI}" in
  http://localhost:[0-9]*|http://127.0.0.1:[0-9]*) ;;
  *) die "FLOCI=${FLOCI} is not a loopback endpoint; the races only run against a local floci" ;;
esac
[ -x "${PLUNGE}" ] || die "plunge binary not found at ${PLUNGE} (run 'make plunge' or set PLUNGE)"
PLUNGE="$(cd "$(dirname "${PLUNGE}")" && pwd)/$(basename "${PLUNGE}")"
command -v aws >/dev/null || die "the aws CLI is required (it only ever talks to ${FLOCI})"
command -v python3 >/dev/null || die "python3 is required"

# Offline: no real cloud credentials reach plunge, a provider or the aws CLI.
for v in $(env | sed -n 's/^\(AWS_[A-Z_]*\)=.*/\1/p;s/^\(AZURE_[A-Z_]*\)=.*/\1/p;s/^\(ARM_[A-Z_]*\)=.*/\1/p;s/^\(GOOGLE_[A-Z_]*\)=.*/\1/p'); do
  unset "${v}"
done
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1 AWS_DEFAULT_REGION=us-east-1
export AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_CONFIG_FILE=/dev/null AWS_EC2_METADATA_DISABLED=true
export AWS_ENDPOINT_URL="${FLOCI}"
# The bind phase's Cloud Control lister (pkg/backend/plunge/listers.go) calls floci too.
export PLUNGE_CLOUDCONTROL_ENDPOINT="${FLOCI}"
export PULUMI_SKIP_UPDATE_CHECK=true PULUMI_SKIP_CONFIRMATIONS=true
unset PULUMI_ACCESS_TOKEN PULUMI_BACKEND_URL

awsf() { aws --endpoint-url "${FLOCI}" --output json "$@"; }

aws sts get-caller-identity --endpoint-url "${FLOCI}" >/dev/null 2>&1 ||
  die "floci is not answering at ${FLOCI} (start it: docker run -d --rm -p 127.0.0.1:4566:4566 \"\$(cat <choudoufu>/live/floci-image)\")"

mkdir -p "${OUT}"
VERDICTS="${OUT}/verdicts.txt"
touch "${VERDICTS}"
{
  echo "plunge: ${PLUNGE}"
  "${PLUNGE}" version 2>&1 | sed 's/^/  /'
  echo "floci:  ${FLOCI}"
  echo "tree:   $(git -C "${ROOT}" rev-parse HEAD 2>/dev/null || echo unknown)"
} > "${OUT}/versions.txt"

# verdict <claim> <race> <PASS|FAIL> <why>: one line per claim, in ${OUT}/verdicts.txt and on stdout.
verdict() {
  echo "VERDICT $1 $2 $3 $4" | tee -a "${VERDICTS}"
}

# secure_bucket <bucket>: the settings plunge+s3:// checks on open (INTENTIUS/plunge#20, #34), so the race runs with
# no PLUNGE_ALLOW_INSECURE waiver: versioning on, an enabled lifecycle rule with no filter that expires noncurrent
# versions after 30 days, and all four public access block settings on. The rule deletes no current object.
secure_bucket() {
  awsf s3api put-bucket-versioning --bucket "$1" --versioning-configuration Status=Enabled >/dev/null
  awsf s3api put-bucket-lifecycle-configuration --bucket "$1" --lifecycle-configuration \
    '{"Rules":[{"ID":"records-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' \
    >/dev/null
  awsf s3api put-public-access-block --bucket "$1" --public-access-block-configuration \
    BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null
}

# race_begin <name>: a fresh bucket, prefix and backend for one race. Sets RACE, WORK, BUCKET, PREFIX, BACKEND.
race_begin() {
  RACE="$1"
  WORK="${OUT}/${RACE}"
  mkdir -p "${WORK}"
  local stamp
  stamp="$(date -u +%H%M%S)$((RANDOM % 1000))"
  BUCKET="plr-${RACE}-${stamp}"
  PREFIX="plr${RACE//[^a-z0-9]/}${stamp}"
  awsf s3api create-bucket --bucket "${BUCKET}" >/dev/null
  secure_bucket "${BUCKET}"
  BACKEND="plunge+s3://${BUCKET}?region=us-east-1&endpoint=${FLOCI}&s3ForcePathStyle=true"
  echo "== ${RACE}: bucket ${BUCKET}, prefix ${PREFIX}" | tee "${WORK}/race.txt"
}

# write_program <dir> <resource>...: the race program with only the named resources. Every physical name carries
# ${PREFIX}. Knobs are stack config: retentionA, retentionB, retentionW. native-site and native-role are aws-native
# types (INTENTIUS/plunge#36), created through Cloud Control; their names are their primary identifiers, so plunge's
# C1 autonaming covers them as it covers site and role.
write_program() {
  local dir="$1"
  shift
  mkdir -p "${dir}"
  python3 -I - "${dir}" "${PREFIX}" "$@" <<'PY'
import sys, pathlib
d, prefix, names = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3:]
V = "      version: 7.51.1\n"
N = "      version: 1.83.1\n"
POLICY = '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}'
res = {
  "site": "  site:\n    type: aws:s3:Bucket\n    properties:\n      bucket: ${prefix}-site\n      tags: {race: plunge}\n    options:\n" + V,
  "role": "  role:\n    type: aws:iam:Role\n    properties:\n      name: ${prefix}-app\n      assumeRolePolicy: '{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"Service\":\"lambda.amazonaws.com\"},\"Action\":\"sts:AssumeRole\"}]}'\n      tags: {race: plunge}\n    options:\n" + V,
  "native-site": "  native-site:\n    type: aws-native:s3:Bucket\n    properties:\n      bucketName: ${prefix}-nsite\n      tags: [{key: race, value: plunge}]\n    options:\n" + N,
  "native-role": "  native-role:\n    type: aws-native:iam:Role\n    properties:\n      roleName: ${prefix}-napp\n      assumeRolePolicyDocument: " + POLICY + "\n      tags: [{key: race, value: plunge}]\n    options:\n" + N,
  "net": "  net:\n    type: aws:ec2:Vpc\n    properties:\n      cidrBlock: 10.77.0.0/16\n      tags: {race: plunge}\n    options:\n" + V,
  "queue-a": "  queue-a:\n    type: aws:sqs:Queue\n    properties:\n      name: ${prefix}-a\n      messageRetentionSeconds: ${retentionA}\n      tags: {race: plunge}\n    options:\n" + V,
  "queue-b": "  queue-b:\n    type: aws:sqs:Queue\n    properties:\n      name: ${prefix}-b\n      messageRetentionSeconds: ${retentionB}\n      tags: {race: plunge}\n    options:\n" + V,
  "worker-1": "  worker-1:\n    type: aws:sqs:Queue\n    properties:\n      name: ${prefix}-w1\n      messageRetentionSeconds: ${retentionW}\n      tags: {race: plunge}\n    options:\n" + V,
}
for i in range(6):
  res[f"q-{i}"] = f"  q-{i}:\n    type: aws:sqs:Queue\n    properties:\n      name: ${{prefix}}-q{i}\n      tags: {{race: plunge}}\n    options:\n" + V
body = "".join(res[n] for n in names)
(d / "Pulumi.yaml").write_text(f"""# plunge race program (scripts/plunge-races, INTENTIUS/plunge#7). Generated by lib.sh's write_program.
name: plunge-races
runtime: yaml
config:
  prefix: {{type: string, default: {prefix}}}
  retentionA: {{type: integer, default: 3600}}
  retentionB: {{type: integer, default: 3600}}
  retentionW: {{type: integer, default: 3600}}
resources:
{body}""")
PY
}

# stack_config <dir> [key=value...]: the floci stack file for one checkout, knobs set.
stack_config() {
  local dir="$1"
  shift
  {
    cat <<YAML
config:
  aws:region: us-east-1
  aws:accessKey: test
  aws:secretKey: test
  aws:skipCredentialsValidation: true
  aws:skipRequestingAccountId: true
  aws:skipRegionValidation: true
  aws:skipMetadataApiCheck: true
  aws:s3UsePathStyle: true
  aws:endpoints:
    - ec2: ${FLOCI}
      iam: ${FLOCI}
      s3: ${FLOCI}
      sqs: ${FLOCI}
      sts: ${FLOCI}
  aws-native:region: us-east-1
YAML
    local kv
    for kv in "$@"; do
      echo "  plunge-races:${kv%%=*}: ${kv#*=}"
    done
  } > "${dir}/Pulumi.${STACK}.yaml.tmp"
  # Keep the encryptionsalt `stack init` wrote, so both checkouts decrypt alike.
  if [ -f "${dir}/Pulumi.${STACK}.yaml" ]; then
    grep '^encryptionsalt:' "${dir}/Pulumi.${STACK}.yaml" >> "${dir}/Pulumi.${STACK}.yaml.tmp" || true
  fi
  mv "${dir}/Pulumi.${STACK}.yaml.tmp" "${dir}/Pulumi.${STACK}.yaml"
}

# pl <dir> <log> <args...>: plunge in a checkout against the race's backend. The log gets stdout and stderr; the
# exit code is returned.
pl() {
  local dir="$1" log="$2"
  shift 2
  (cd "${dir}" && PULUMI_BACKEND_URL="${BACKEND}" "${PLUNGE}" "$@" --stack "${STACK}" --non-interactive) \
    >>"${log}" 2>&1
}

# checkout <dir> <resource>...: a checkout of the race program; the first one creates the stack.
checkout() {
  local dir="$1"
  shift
  write_program "${dir}" "$@"
  stack_config "${dir}"
  if [ ! -f "${WORK}/.stack-created" ]; then
    (cd "${dir}" && PULUMI_BACKEND_URL="${BACKEND}" "${PLUNGE}" stack init "${STACK}" \
      --secrets-provider passphrase --non-interactive) >>"${WORK}/setup.log" 2>&1
    cp "${dir}/Pulumi.${STACK}.yaml" "${WORK}/.stack-file"
    touch "${WORK}/.stack-created"
  else
    grep '^encryptionsalt:' "${WORK}/.stack-file" >> "${dir}/Pulumi.${STACK}.yaml" || true
  fi
}

# records: the race's record listing, one line per record: kind, decoded name, size. Written to ${WORK}/records.txt.
records() {
  awsf s3api list-objects-v2 --bucket "${BUCKET}" --prefix ".pulumi/records/" > "${WORK}/records.json"
  python3 -I - "${WORK}/records.json" <<'PY' | tee "${WORK}/records.txt"
import base64, json, sys
raw = open(sys.argv[1]).read().strip()
objs = json.loads(raw).get("Contents", []) if raw else []
for o in sorted(objs, key=lambda o: o["Key"]):
  parts = o["Key"].split("/")
  name = parts[-1]
  if len(parts) >= 2 and parts[-2] in ("resources", "outputs", "snippets", "extensions"):
    try:
      name = base64.urlsafe_b64decode(name + "=" * (-len(name) % 4)).decode()
    except Exception:
      pass
    print(f"{parts[-2]} {name} {o['Size']}")
  else:
    print(f"{'/'.join(parts[2:])} {o['Size']}")
PY
}

# record_of <urn-name>: the JSON of the resource record whose URN ends in ::<urn-name>, on stdout.
record_of() {
  local key
  key="$(python3 -I - "${WORK}/records.json" "$1" <<'PY'
import base64, json, sys
objs = json.loads(open(sys.argv[1]).read() or "{}").get("Contents", [])
for o in objs:
  parts = o["Key"].split("/")
  if len(parts) >= 2 and parts[-2] == "resources":
    n = parts[-1]
    urn = base64.urlsafe_b64decode(n + "=" * (-len(n) % 4)).decode()
    if urn.endswith("::" + sys.argv[2]):
      print(o["Key"])
PY
)"
  [ -n "${key}" ] || return 1
  awsf s3 cp "s3://${BUCKET}/${key}" - 2>/dev/null
}

# no_changes <log>: the `preview --json` in <log> plans nothing but same steps.
no_changes() {
  python3 -I - "$1" <<'PY'
import json, sys
raw = open(sys.argv[1]).read()
start = raw.find("{")
doc = json.loads(raw[start:]) if start >= 0 else {}
ops = sorted({s.get("op") for s in doc.get("steps") or []} - {"same"})
summary = {k: v for k, v in (doc.get("changeSummary") or {}).items() if k != "same"}
sys.exit(0 if not ops and not summary else 1)
PY
}

# real_cloud_rejections <log>...: the lines in the logs where a real AWS endpoint answered or refused our test keys.
# The aws-native provider takes its endpoint from AWS_ENDPOINT_URL (exported above), not from stack config, so a
# race over aws-native types checks for these before reading anything else.
real_cloud_rejections() {
  grep -hE 'UnrecognizedClientException|InvalidClientTokenId|SignatureDoesNotMatch|ExpiredToken|https://[a-z0-9.-]+\.amazonaws\.com' "$@" \
    2>/dev/null || true
}

# lockish_keys: every key in the race bucket that looks like a lock, plus the DIY backend's lock directory.
lockish_keys() {
  awsf s3api list-objects-v2 --bucket "${BUCKET}" --query 'Contents[].Key' --output text 2>/dev/null |
    tr '\t' '\n' | grep -iE '(^|/)\.pulumi/locks/|lock' || true
}
