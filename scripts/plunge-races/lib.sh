#!/usr/bin/env bash
#
# Shared helpers for the plunge race scripts (INTENTIUS/plunge#7). Sourced, never run. The maintainer runs the
# scripts by hand against a local floci; by default nothing here talks to AWS or Pulumi Cloud. PLUNGE_RACES_TARGET=aws is the
# one explicit exception (below).
#
# Environment:
#   PLUNGE   plunge binary           (default: bin/plunge)
#   PULUMI   stock pulumi binary     (default: pulumi on PATH; adopt-and-back-out.sh only)
#   FLOCI    floci endpoint          (default: http://localhost:4566; loopback only)
#   OUT      output directory        (default: plunge-races-out/<UTC timestamp>)
#
#   PLUNGE_RACES_TARGET   floci (default) or aws. aws is an explicit opt-in to REAL AWS, us-east-1 only, with the
#                         caller's own credentials from the default chain. Nothing is scrubbed and no endpoint is
#                         overridden. Every resource name carries the run prefix (plr...), and after each race a
#                         cleanup deletes only what carries that prefix. See aws_cleanup below.
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

TARGET="${PLUNGE_RACES_TARGET:-floci}"
case "${TARGET}" in
  floci|aws) ;;
  *) die "PLUNGE_RACES_TARGET=${TARGET} is not floci or aws" ;;
esac
[ -x "${PLUNGE}" ] || die "plunge binary not found at ${PLUNGE} (run 'make plunge' or set PLUNGE)"
PLUNGE="$(cd "$(dirname "${PLUNGE}")" && pwd)/$(basename "${PLUNGE}")"
command -v python3 >/dev/null || die "python3 is required"
command -v aws >/dev/null || die "the aws CLI is required"

if [ "${TARGET}" = aws ]; then
  # Real AWS: the caller's credentials stay, in us-east-1, with no endpoint override anywhere. A stray
  # AWS_ENDPOINT_URL or a Cloud Control endpoint would send part of the run somewhere else, so they are removed.
  unset AWS_ENDPOINT_URL PLUNGE_CLOUDCONTROL_ENDPOINT AWS_ENDPOINT_URL_S3 AWS_ENDPOINT_URL_SQS AWS_ENDPOINT_URL_IAM \
    AWS_ENDPOINT_URL_EC2 AWS_ENDPOINT_URL_STS AWS_ENDPOINT_URL_CLOUDCONTROL
  export AWS_REGION=us-east-1 AWS_DEFAULT_REGION=us-east-1
  export PULUMI_SKIP_UPDATE_CHECK=true PULUMI_SKIP_CONFIRMATIONS=true
  unset PULUMI_ACCESS_TOKEN PULUMI_BACKEND_URL
  awsf() { aws --output json "$@"; }
  CALLER="$(aws sts get-caller-identity --output json 2>/dev/null)" ||
    die "no usable AWS credentials in the default chain (aws sts get-caller-identity failed)"
  ACCOUNT="$(python3 -I -c 'import json,sys; print(json.load(sys.stdin)["Account"])' <<<"${CALLER}")"
  if [ -n "${PLUNGE_RACES_AWS_ACCOUNT:-}" ] && [ "${PLUNGE_RACES_AWS_ACCOUNT}" != "${ACCOUNT}" ]; then
    die "caller is account ${ACCOUNT}, but PLUNGE_RACES_AWS_ACCOUNT=${PLUNGE_RACES_AWS_ACCOUNT}"
  fi
else
  case "${FLOCI}" in
    http://localhost:[0-9]*|http://127.0.0.1:[0-9]*) ;;
    *) die "FLOCI=${FLOCI} is not a loopback endpoint; the races only run against a local floci" ;;
  esac

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
fi

mkdir -p "${OUT}"
VERDICTS="${OUT}/verdicts.txt"
touch "${VERDICTS}"
{
  echo "plunge: ${PLUNGE}"
  "${PLUNGE}" version 2>&1 | sed 's/^/  /'
  if [ "${TARGET}" = aws ]; then
    echo "target: real AWS, us-east-1 (PLUNGE_RACES_TARGET=aws)"
    echo "caller: $(python3 -I -c 'import json,sys; d=json.load(sys.stdin); print(d["Arn"], "account", d["Account"])' <<<"${CALLER}")"
    echo "aws:    $(aws --version 2>&1)"
  else
    echo "floci:  ${FLOCI}"
  fi
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

# race_begin <name>: a fresh bucket, prefix, stack and backend for one race. Sets RACE, WORK, BUCKET, PREFIX, STACK,
# BACKEND. The stack name goes into every object's plunge-stack marker, so a stack per race keeps one race's bind
# phase from binding objects an earlier race left on the same floci.
AWS_BEGUN=()
race_begin() {
  RACE="$1"
  WORK="${OUT}/${RACE}"
  mkdir -p "${WORK}"
  local stamp
  stamp="$(date -u +%H%M%S)$((RANDOM % 1000))"
  if [ "${TARGET}" = aws ]; then
    # Bucket names are global: the account id and a random suffix keep them unique. The prefix stays short (an
    # IAM role name is capped at 64 characters) and starts with plr, which is all the cleanup will ever match.
    local rnd
    rnd="$(python3 -I -c 'import secrets; print(secrets.token_hex(3))')"
    stamp="${stamp}${rnd}"
    BUCKET="plr-${RACE}-${ACCOUNT}-${stamp}"
  else
    BUCKET="plr-${RACE}-${stamp}"
  fi
  PREFIX="plr${RACE//[^a-z0-9]/}${stamp}"
  STACK="race-${RACE}-${stamp}"
  awsf s3api create-bucket --bucket "${BUCKET}" >/dev/null
  secure_bucket "${BUCKET}"
  if [ "${TARGET}" = aws ]; then
    BACKEND="plunge+s3://${BUCKET}?region=us-east-1"
    AWS_BEGUN+=("${RACE}|${BUCKET}|${PREFIX}|${STACK}|${WORK}")
  else
    BACKEND="plunge+s3://${BUCKET}?region=us-east-1&endpoint=${FLOCI}&s3ForcePathStyle=true"
  fi
  echo "== ${RACE}: bucket ${BUCKET}, prefix ${PREFIX}, stack ${STACK}" | tee "${WORK}/race.txt"
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
# The readback race's RandomStrings (pulumi-random, no cloud), as the retired acceptance script had them:
# a keeper from config, which pulumi-random's Read hands back in its own form.
for k in ("x", "y"):
  res[f"rand-{k}"] = f"  rand-{k}:\n    type: random:RandomString\n    properties:\n      length: 12\n      special: false\n      keepers:\n        v: ${{keeper{k.upper()}}}\n"
body = "".join(res[n] for n in names)
# Keeper knobs only when a RandomString is in the program, so every other race's Pulumi.yaml stays as recorded.
keepers = "".join(f"  keeper{k.upper()}: {{type: string, default: \"1\"}}\n" for k in ("x", "y") if f"rand-{k}" in names)
(d / "Pulumi.yaml").write_text(f"""# plunge race program (scripts/plunge-races, INTENTIUS/plunge#7). Generated by lib.sh's write_program.
name: plunge-races
runtime: yaml
config:
  prefix: {{type: string, default: {prefix}}}
  retentionA: {{type: integer, default: 3600}}
  retentionB: {{type: integer, default: 3600}}
  retentionW: {{type: integer, default: 3600}}
{keepers}resources:
{body}""")
PY
}

# stack_config <dir> [key=value...]: the floci stack file for one checkout, knobs set.
stack_config() {
  local dir="$1"
  shift
  {
    if [ "${TARGET}" = aws ]; then
      cat <<YAML
config:
  aws:region: us-east-1
  aws-native:region: us-east-1
YAML
    else
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
    fi
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

# record_versions <urn-name>...: for each named resource, the number of stored versions of its record (the race
# bucket is versioned, so every write of a record adds one). One "<name> <count>" line per name.
record_versions() {
  awsf s3api list-object-versions --bucket "${BUCKET}" --prefix ".pulumi/records/" > "${WORK}/record-versions.json"
  python3 -I - "${WORK}/record-versions.json" "$@" <<'PY'
import base64, collections, json, sys
raw = open(sys.argv[1]).read().strip()
doc = json.loads(raw) if raw else {}
count = collections.Counter()
for v in doc.get("Versions") or []:
  parts = v["Key"].split("/")
  if len(parts) >= 2 and parts[-2] == "resources":
    n = parts[-1]
    count[base64.urlsafe_b64decode(n + "=" * (-len(n) % 4)).decode().rsplit("::", 1)[-1]] += 1
for name in sys.argv[2:]:
  print(name, count[name])
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

# no_changes <log>: the `preview --json` in <log> plans nothing but same steps. Since plunge refreshes by default,
# the preview also lists a `refresh` step per resource; one with no diff is a read, not a change, and any drift it
# finds shows as a later update step.
no_changes() {
  python3 -I - "$1" <<'PY'
import json, sys
raw = open(sys.argv[1]).read()
start = raw.find("{")
# The log holds stdout and stderr together, so plunge's own warnings can follow the JSON document; read the first value.
doc = json.JSONDecoder().raw_decode(raw[start:])[0] if start >= 0 else {}
def change(s):
  if s.get("op") == "same":
    return False
  return not (s.get("op") == "refresh" and not s.get("diffReasons") and not s.get("detailedDiff"))
ops = sorted({s.get("op") for s in doc.get("steps") or [] if change(s)})
summary = {k: v for k, v in (doc.get("changeSummary") or {}).items() if k != "same"}
sys.exit(0 if not ops and not summary else 1)
PY
}

# real_cloud_rejections <log>...: the lines in the logs where a real AWS endpoint answered or refused our test keys.
# The aws-native provider takes its endpoint from AWS_ENDPOINT_URL (exported above), not from stack config, so a
# race over aws-native types checks for these before reading anything else.
real_cloud_rejections() {
  if [ "${TARGET}" = aws ]; then
    # Real AWS answering from amazonaws.com is the point of this mode. What still fails a race is the cloud
    # refusing the caller's credentials.
    grep -hE 'UnrecognizedClientException|InvalidClientTokenId|SignatureDoesNotMatch|ExpiredToken' "$@" 2>/dev/null || true
    return
  fi
  grep -hE 'UnrecognizedClientException|InvalidClientTokenId|SignatureDoesNotMatch|ExpiredToken|https://[a-z0-9.-]+\.amazonaws\.com' "$@" \
    2>/dev/null || true
}

# live_queues <name-prefix>: how many queues carry the prefix. SQS list-queues lags a delete by up to a minute on
# real AWS, so in aws mode this waits (up to 120 s) for the count to reach 0 and returns what it last saw; on floci it
# lists once, as before.
live_queues() {
  local n i
  for i in $(seq 1 24); do
    n="$(awsf sqs list-queues --queue-name-prefix "$1" --query 'QueueUrls' --output text 2>/dev/null | sed 's/None//' | wc -w | tr -d ' ')"
    { [ "${TARGET}" = aws ] && [ "${n}" != 0 ] && [ "${i}" -lt 24 ]; } || break
    sleep 5
  done
  echo "${n}"
}

# lockish_keys: every key in the race bucket that looks like a lock, plus the DIY backend's lock directory.
lockish_keys() {
  awsf s3api list-objects-v2 --bucket "${BUCKET}" --query 'Contents[].Key' --output text 2>/dev/null |
    tr '\t' '\n' | grep -iE '(^|/)\.pulumi/locks/|lock' || true
}

# ---- aws mode only: cleanup and the leftover check -------------------------------------------------------------
#
# Every resource a race creates carries its run prefix (plr<race><stamp>): buckets, queues and roles by name, VPCs by
# the plunge-stack tag naming the race's stack plus race=plunge. The record store bucket is plr-<race>-<account>-<stamp>.
# Nothing is deleted unless its name or tag matches, and the prefix itself must look like plr + lowercase/digits.

# aws_empty_bucket <bucket>: delete every object version and delete marker, then the bucket.
aws_delete_bucket() {
  local b="$1"
  case "${b}" in plr-*|plr[a-z0-9]*) ;; *) >&2 echo "cleanup: refusing bucket ${b}"; return 0 ;; esac
  aws s3api head-bucket --bucket "${b}" >/dev/null 2>&1 || return 0
  python3 -I - "${b}" <<'PY'
import json, subprocess, sys
b = sys.argv[1]
def aws(*a):
  r = subprocess.run(["aws", "--output", "json", *a], capture_output=True, text=True)
  return json.loads(r.stdout) if r.stdout.strip() else {}
while True:
  doc = aws("s3api", "list-object-versions", "--bucket", b, "--max-items", "1000")
  objs = [{"Key": v["Key"], "VersionId": v["VersionId"]} for v in (doc.get("Versions") or []) + (doc.get("DeleteMarkers") or [])]
  if not objs:
    break
  subprocess.run(["aws", "s3api", "delete-objects", "--bucket", b, "--delete", json.dumps({"Objects": objs, "Quiet": True})],
                 capture_output=True, text=True)
PY
  aws s3api delete-bucket --bucket "${b}" 2>&1 || true
}

# aws_cleanup <race> <bucket> <prefix> <stack> <work>: destroy the race's stack if records remain, then delete what
# carries the prefix, then the record store. Output goes to <work>/cleanup.log.
aws_cleanup() {
  local race="$1" bucket="$2" prefix="$3" stack="$4" work="$5" log="$5/cleanup.log"
  case "${prefix}" in plr[a-z0-9]*) ;; *) echo "cleanup: refusing prefix ${prefix}" >> "${log}"; return 0 ;; esac
  case "${bucket}" in plr-*) ;; *) echo "cleanup: refusing bucket ${bucket}" >> "${log}"; return 0 ;; esac
  {
    echo "== cleanup ${race} (prefix ${prefix}, stack ${stack}) $(date -u +%H:%M:%SZ)"
    local d
    for d in a c b; do
      if [ -f "${work}/${d}/Pulumi.yaml" ] && [ -f "${work}/${d}/Pulumi.${stack}.yaml" ]; then
        if aws s3api list-objects-v2 --bucket "${bucket}" --prefix ".pulumi/records/" --max-keys 1 \
          --query 'length(Contents || `[]`)' --output text 2>/dev/null | grep -qv '^0$'; then
          echo "-- destroy from ${d}"
          (cd "${work}/${d}" && PULUMI_BACKEND_URL="plunge+s3://${bucket}?region=us-east-1" \
            "${PLUNGE}" destroy --yes --stack "${stack}" --non-interactive 2>&1 | tail -20) || true
        fi
        break
      fi
    done
    # S3 buckets created by the race program.
    local n
    for n in $(aws s3api list-buckets --query "Buckets[?starts_with(Name, \`${prefix}\`)].Name" --output text); do
      echo "-- delete bucket ${n}"; aws_delete_bucket "${n}"
    done
    # SQS queues.
    local u
    for u in $(aws sqs list-queues --region us-east-1 --queue-name-prefix "${prefix}" --query 'QueueUrls' --output text 2>/dev/null | sed 's/None//'); do
      echo "-- delete queue ${u}"; aws sqs delete-queue --region us-east-1 --queue-url "${u}" || true
    done
    # IAM roles (no policies are attached by the race program, but detach anything found).
    for n in $(aws iam list-roles --query "Roles[?starts_with(RoleName, \`${prefix}\`)].RoleName" --output text); do
      echo "-- delete role ${n}"
      for p in $(aws iam list-attached-role-policies --role-name "${n}" --query 'AttachedPolicies[].PolicyArn' --output text); do
        aws iam detach-role-policy --role-name "${n}" --policy-arn "${p}" || true
      done
      for p in $(aws iam list-role-policies --role-name "${n}" --query 'PolicyNames[]' --output text); do
        aws iam delete-role-policy --role-name "${n}" --policy-name "${p}" || true
      done
      aws iam delete-role --role-name "${n}" || true
    done
    # VPCs: tagged race=plunge and carrying this stack's plunge-stack marker.
    local v
    for v in $(aws ec2 describe-vpcs --region us-east-1 --filters "Name=tag:race,Values=plunge" "Name=tag-key,Values=plunge-urn" \
      --query 'Vpcs[].VpcId' --output text); do
      if aws ec2 describe-vpcs --region us-east-1 --vpc-ids "${v}" --query 'Vpcs[0].Tags[?Key==`plunge-stack`].Value' --output text |
        grep -qxF "plunge-races/${stack}"; then
        echo "-- delete vpc ${v}"; aws ec2 delete-vpc --region us-east-1 --vpc-id "${v}" || true
      fi
    done
    # The record store last.
    echo "-- delete record store ${bucket}"
    aws_delete_bucket "${bucket}"
  } >> "${log}" 2>&1
}

# aws_cleanup_all: clean every race begun and not yet cleaned (the main loop calls it after each race; an EXIT trap
# calls it again so an aborted run still cleans up).
AWS_CLEANED=0
aws_cleanup_all() {
  [ "${TARGET}" = aws ] || return 0
  local i rec
  for ((i = AWS_CLEANED; i < ${#AWS_BEGUN[@]}; i++)); do
    IFS='|' read -r r b p s w <<<"${AWS_BEGUN[$i]}"
    echo "cleanup: ${r}"
    aws_cleanup "${r}" "${b}" "${p}" "${s}" "${w}"
  done
  AWS_CLEANED=${#AWS_BEGUN[@]}
}

# aws_leftovers: what is still in the account in us-east-1 under the run's naming: plr* buckets, plr* queues, plr*
# roles, and VPCs tagged race=plunge. Written to ${OUT}/leftovers.txt; "none" when empty.
aws_leftovers() {
  [ "${TARGET}" = aws ] || return 0
  {
    echo "buckets:"; aws s3api list-buckets --query 'Buckets[?starts_with(Name, `plr`)].Name' --output text | tr '\t' '\n' | sed '/^$/d'
    echo "queues:"; aws sqs list-queues --region us-east-1 --queue-name-prefix plr --query 'QueueUrls' --output text | tr '\t' '\n' | sed '/^$/d;/^None$/d'
    echo "roles:"; aws iam list-roles --query 'Roles[?starts_with(RoleName, `plr`)].RoleName' --output text | tr '\t' '\n' | sed '/^$/d'
    echo "vpcs:"; aws ec2 describe-vpcs --region us-east-1 --filters "Name=tag:race,Values=plunge" --query 'Vpcs[].VpcId' --output text | tr '\t' '\n' | sed '/^$/d'
  } > "${OUT}/leftovers.txt" 2>&1
  if [ "$(grep -vcE '^(buckets|queues|roles|vpcs):$' "${OUT}/leftovers.txt")" = 0 ]; then
    echo "leftovers: none" | tee -a "${OUT}/leftovers.txt"
  else
    echo "leftovers found:"; cat "${OUT}/leftovers.txt"
  fi
}
