#!/usr/bin/env bash
#
# INTENTIUS/plunge#45, the AWS part: an IAM policy conditioned on plunge's plunge-stack tag, run against REAL AWS.
#
# Opt-in. This script creates and deletes real resources (an S3 record-store bucket, three SQS queues, one IAM
# policy and one IAM role, all named plg-gate-<stamp> or plg<stamp>-*) in us-east-1 with the credentials on the
# machine. Nothing else is touched; the cleanup and the leftover check only look at the plg prefix.
#
# Usage:
#   PLUNGE_GATE_REAL_AWS=yes PLUNGE=/path/to/plunge scripts/plunge-gate/aws-iam.sh
#
# Environment:
#   PLUNGE_GATE_REAL_AWS  must be "yes"
#   PLUNGE                plunge binary (required; a released binary, not a build of this tree)
#   OUT                   output directory (default: tests/plunge-gate/aws-iam)
#   AWS_*                 the default credential chain; the caller must be allowed to create roles and policies
#
# Shape: stack A and stack B are two stacks of one program with one SQS queue each. A role assumable by the
# caller gets a policy that allows SQS writes only where aws:ResourceTag/plunge-stack is stack A's value and
# creates only with aws:RequestTag/plunge-stack set to it. plunge then runs as that role.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
die() { >&2 echo "plunge-gate: $*"; exit 2; }

[ "${PLUNGE_GATE_REAL_AWS:-}" = yes ] || die "set PLUNGE_GATE_REAL_AWS=yes: this script uses real AWS"
PLUNGE="${PLUNGE:-}"
[ -f "${PLUNGE}" ] && [ -x "${PLUNGE}" ] || die "set PLUNGE to a plunge binary"
PLUNGE="$(cd "$(dirname "${PLUNGE}")" && pwd)/$(basename "${PLUNGE}")"
command -v aws >/dev/null || die "the aws CLI is required"
command -v python3 >/dev/null || die "python3 is required"
OUT="${OUT:-${ROOT}/tests/plunge-gate/aws-iam}"

# Admin credentials: whatever the default chain gives. Pin the region and the plunge settings.
unset AWS_PROFILE AWS_DEFAULT_PROFILE AWS_ENDPOINT_URL
export AWS_REGION=us-east-1 AWS_DEFAULT_REGION=us-east-1
export PULUMI_SKIP_UPDATE_CHECK=true PULUMI_SKIP_CONFIRMATIONS=true PULUMI_CONFIG_PASSPHRASE=plunge-gate
unset PULUMI_ACCESS_TOKEN PULUMI_BACKEND_URL

IDENT="$(aws sts get-caller-identity --output json)" || die "no AWS credentials"
ACCOUNT="$(python3 -I -c 'import json,sys;print(json.loads(sys.argv[1])["Account"])' "${IDENT}")"
CALLER_ARN="$(python3 -I -c 'import json,sys;print(json.loads(sys.argv[1])["Arn"])' "${IDENT}")"

STAMP="$(date -u +%H%M%S)$((RANDOM % 100))"
NAME="plg-gate-${STAMP}"
PFX="plg${STAMP}"
BUCKET="${NAME}"
PROJECT=plunge-gate
STACK_A="gate-a-${STAMP}"
STACK_B="gate-b-${STAMP}"
STACK_C="gate-c-${STAMP}"
VALUE_A="${PROJECT}/${STACK_A}"
BACKEND="plunge+s3://${BUCKET}?region=us-east-1"
POLICY_ARN="arn:aws:iam::${ACCOUNT}:policy/${NAME}"
ROLE_ARN="arn:aws:iam::${ACCOUNT}:role/${NAME}"

find "${OUT}" -mindepth 1 -delete 2>/dev/null
mkdir -p "${OUT}"
VERDICTS="${OUT}/verdicts.txt"
: > "${VERDICTS}"
verdict() { echo "VERDICT $1 $2 $3" | tee -a "${VERDICTS}"; }
note() { echo "== $*" | tee -a "${OUT}/run.log"; }
{
  echo "plunge: ${PLUNGE}"
  "${PLUNGE}" version 2>&1 | sed 's/^/  /'
  echo "aws:    $(aws --version 2>&1)"
  echo "region: us-east-1"
  echo "prefix: ${PFX} (bucket, role and policy ${NAME})"
  echo "stacks: ${VALUE_A} (allowed), ${PROJECT}/${STACK_B} and ${PROJECT}/${STACK_C} (not)"
  echo "date:   $(date -u +%Y-%m-%dT%H:%M:%SZ)"
} > "${OUT}/versions.txt"

# --- cleanup -----------------------------------------------------------------------------------------------
purge_bucket() {
  python3 -I - "$1" <<'PY'
import json, subprocess, sys
b = sys.argv[1]
def aws(*a):
  r = subprocess.run(["aws", "--output", "json", *a], capture_output=True, text=True)
  return json.loads(r.stdout) if r.stdout.strip() else {}
while True:
  d = aws("s3api", "list-object-versions", "--bucket", b, "--max-items", "500")
  objs = [{"Key": v["Key"], "VersionId": v["VersionId"]} for v in (d.get("Versions") or []) + (d.get("DeleteMarkers") or [])]
  if not objs:
    break
  subprocess.run(["aws", "s3api", "delete-objects", "--bucket", b, "--delete", json.dumps({"Objects": objs, "Quiet": True})],
                 capture_output=True)
PY
}

CLEANED=0
cleanup() {
  [ "${CLEANED}" = 1 ] && return
  CLEANED=1
  note "cleanup: stacks, queues, role, policy, bucket"
  unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
  for s in "${STACK_A}:a" "${STACK_B}:b" "${STACK_C}:c"; do
    [ -d "${WORK}/${s#*:}" ] && (cd "${WORK}/${s#*:}" && PULUMI_BACKEND_URL="${BACKEND}" "${PLUNGE}" destroy --stack "${s%%:*}" \
      --yes --non-interactive >>"${OUT}/cleanup.log" 2>&1)
  done
  for q in a b c; do
    url="$(aws sqs get-queue-url --queue-name "${PFX}-${q}" --query QueueUrl --output text 2>/dev/null)" &&
      aws sqs delete-queue --queue-url "${url}" >>"${OUT}/cleanup.log" 2>&1
  done
  aws iam detach-role-policy --role-name "${NAME}" --policy-arn "${POLICY_ARN}" >>"${OUT}/cleanup.log" 2>&1
  aws iam delete-role --role-name "${NAME}" >>"${OUT}/cleanup.log" 2>&1
  aws iam delete-policy --policy-arn "${POLICY_ARN}" >>"${OUT}/cleanup.log" 2>&1
  purge_bucket "${BUCKET}"
  aws s3api delete-bucket --bucket "${BUCKET}" >>"${OUT}/cleanup.log" 2>&1
  leftovers
  redact
}

# leftovers: everything with the plg prefix, in every place the script creates something. Empty is the pass.
leftovers() {
  {
    aws sqs list-queues --queue-name-prefix plg --query 'QueueUrls[]' --output text 2>&1
    aws s3api list-buckets --query 'Buckets[?starts_with(Name, `plg`)].Name' --output text 2>&1
    aws iam list-roles --query 'Roles[?starts_with(RoleName, `plg`)].RoleName' --output text 2>&1
    aws iam list-policies --scope Local --query 'Policies[?starts_with(PolicyName, `plg`)].PolicyName' --output text 2>&1
  } | sed '/^None$/d;/^$/d' > "${OUT}/leftovers.txt"
  if [ -s "${OUT}/leftovers.txt" ]; then
    echo "VERDICT leftovers - FAIL plg-prefixed resources remain: $(tr '\n' ' ' < "${OUT}/leftovers.txt")" | tee -a "${VERDICTS}"
  else
    echo "VERDICT leftovers - PASS no plg queue, bucket, role or policy remains" | tee -a "${VERDICTS}"
  fi
}

# The account id is not needed to read a recording; replace it.
redact() {
  find "${OUT}" -type f \( -name '*.log' -o -name '*.txt' -o -name '*.json' -o -name '*.yaml' \) -print0 |
    xargs -0 sed -i.bak "s/${ACCOUNT}/ACCOUNT/g" 2>/dev/null
  find "${OUT}" -name '*.bak' -delete
}
WORK="${OUT}/work"
mkdir -p "${WORK}"
trap cleanup EXIT

# --- record store ------------------------------------------------------------------------------------------
note "bucket ${BUCKET}"
aws s3api create-bucket --bucket "${BUCKET}" >>"${OUT}/setup.log" 2>&1 || die "bucket create failed"
aws s3api put-bucket-versioning --bucket "${BUCKET}" --versioning-configuration Status=Enabled >>"${OUT}/setup.log"
aws s3api put-bucket-lifecycle-configuration --bucket "${BUCKET}" --lifecycle-configuration \
  '{"Rules":[{"ID":"records-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' >>"${OUT}/setup.log"
aws s3api put-public-access-block --bucket "${BUCKET}" --public-access-block-configuration \
  BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >>"${OUT}/setup.log"

# --- programs ----------------------------------------------------------------------------------------------
# One program, three checkouts (a, b, c) each with its own stack and its own queue name.
for d in a b c; do
  mkdir -p "${WORK}/${d}"
  cat > "${WORK}/${d}/Pulumi.yaml" <<YAML
name: ${PROJECT}
runtime: yaml
config:
  retention: {type: integer, default: 3600}
resources:
  queue:
    type: aws:sqs:Queue
    properties:
      name: ${PFX}-${d}
      messageRetentionSeconds: \${retention}
      tags: {gate: plunge}
    options:
      version: 7.51.1
YAML
done
declare -A STK=([a]="${STACK_A}" [b]="${STACK_B}" [c]="${STACK_C}")

# pl <checkout> <log> <args...>: plunge in a checkout. Credentials are whatever the environment holds.
pl() {
  local d="$1" log="$2"
  shift 2
  (cd "${WORK}/${d}" && PULUMI_BACKEND_URL="${BACKEND}" "${PLUNGE}" "$@" --stack "${STK[$d]}" --non-interactive) >>"${log}" 2>&1
}
for d in a b c; do
  (cd "${WORK}/${d}" && PULUMI_BACKEND_URL="${BACKEND}" "${PLUNGE}" stack init "${STK[$d]}" --secrets-provider passphrase \
    --non-interactive) >>"${OUT}/setup.log" 2>&1 || die "stack init ${d} failed (see ${OUT}/setup.log)"
  pl "$d" "${OUT}/setup.log" config set aws:region us-east-1
done

# --- stack B's queue is made by the caller, outside the role ----------------------------------------------
note "stack B up as the caller"
pl b "${OUT}/b-up.log" up --yes || die "stack B up failed (see ${OUT}/b-up.log)"
qurl_b="$(aws sqs get-queue-url --queue-name "${PFX}-b" --query QueueUrl --output text)"
aws sqs list-queue-tags --queue-url "${qurl_b}" > "${OUT}/b-tags.json"
retention_b() { aws sqs get-queue-attributes --queue-url "${qurl_b}" --attribute-names MessageRetentionPeriod \
  --query Attributes.MessageRetentionPeriod --output text; }

# --- the policy and the role -------------------------------------------------------------------------------
python3 -I - "${VALUE_A}" "${BUCKET}" "${PFX}" "${ACCOUNT}" > "${OUT}/policy.json" <<'PY'
import json, sys
value, bucket, pfx, acct = sys.argv[1:5]
print(json.dumps({
  "Version": "2012-10-17",
  "Statement": [
    {"Sid": "RecordStore", "Effect": "Allow",
     "Action": ["s3:ListBucket", "s3:ListBucketVersions", "s3:GetBucketLocation", "s3:GetBucketVersioning",
                "s3:GetLifecycleConfiguration", "s3:GetBucketPublicAccessBlock", "s3:GetEncryptionConfiguration"],
     "Resource": f"arn:aws:s3:::{bucket}"},
    {"Sid": "RecordStoreObjects", "Effect": "Allow",
     "Action": ["s3:GetObject", "s3:GetObjectVersion", "s3:PutObject", "s3:DeleteObject", "s3:DeleteObjectVersion",
                "s3:GetObjectTagging", "s3:PutObjectTagging"],
     "Resource": f"arn:aws:s3:::{bucket}/*"},
    {"Sid": "ReadQueues", "Effect": "Allow",
     "Action": ["sqs:ListQueues", "sqs:GetQueueUrl", "sqs:GetQueueAttributes", "sqs:ListQueueTags",
                "cloudformation:ListResources", "cloudformation:GetResource"],
     "Resource": "*"},
    {"Sid": "ChangeOnlyThisStack", "Effect": "Allow",
     "Action": ["sqs:SetQueueAttributes", "sqs:DeleteQueue", "sqs:UntagQueue"],
     "Resource": "*",
     "Condition": {"StringEquals": {"aws:ResourceTag/plunge-stack": value}}},
    {"Sid": "CreateOnlyIntoThisStack", "Effect": "Allow",
     "Action": ["sqs:CreateQueue", "sqs:TagQueue"],
     "Resource": "*",
     "Condition": {"StringEquals": {"aws:RequestTag/plunge-stack": value}}},
  ]}, indent=2))
PY
note "policy and role ${NAME}"
aws iam create-policy --policy-name "${NAME}" --policy-document "file://${OUT}/policy.json" >>"${OUT}/setup.log" 2>&1 || die "create-policy failed"
python3 -I - "${CALLER_ARN}" > "${OUT}/trust.json" <<'PY'
import json, sys
print(json.dumps({"Version": "2012-10-17", "Statement": [
  {"Effect": "Allow", "Principal": {"AWS": sys.argv[1]}, "Action": "sts:AssumeRole"}]}, indent=2))
PY
aws iam create-role --role-name "${NAME}" --assume-role-policy-document "file://${OUT}/trust.json" \
  --max-session-duration 3600 >>"${OUT}/setup.log" 2>&1 || die "create-role failed"
aws iam attach-role-policy --role-name "${NAME}" --policy-arn "${POLICY_ARN}" >>"${OUT}/setup.log" 2>&1 || die "attach failed"

# assume the role, retrying while IAM propagates (new roles and trust policies take a few seconds to be visible)
ROLE_CREDS=""
for i in $(seq 1 24); do
  if ROLE_CREDS="$(aws sts assume-role --role-arn "${ROLE_ARN}" --role-session-name plunge-gate \
      --query Credentials --output json 2>>"${OUT}/assume.log")"; then
    echo "assume-role succeeded on attempt ${i}" >> "${OUT}/assume.log"
    break
  fi
  ROLE_CREDS=""
  sleep 5
done
[ -n "${ROLE_CREDS}" ] || die "could not assume ${ROLE_ARN} (see ${OUT}/assume.log)"
role_env() {
  python3 -I - "${ROLE_CREDS}" <<'PY'
import json, sys
c = json.loads(sys.argv[1])
print(f"export AWS_ACCESS_KEY_ID={c['AccessKeyId']} AWS_SECRET_ACCESS_KEY={c['SecretAccessKey']} AWS_SESSION_TOKEN={c['SessionToken']}")
PY
}
# as_role <cmd...>: run a command with the role's credentials (a subshell, so the caller's stay in place)
as_role() { ( eval "$(role_env)"; "$@" ); }
as_caller() { ( unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN; "$@" ); }
# Poll until the policy is in force: the role can read the record store and list queues.
ok=0
for i in $(seq 1 24); do
  if as_role aws sqs list-queues --queue-name-prefix "${PFX}" >/dev/null 2>&1 &&
     as_role aws s3api list-objects-v2 --bucket "${BUCKET}" --max-items 1 >/dev/null 2>&1; then ok=1; break; fi
  sleep 5
done
[ "${ok}" = 1 ] || die "the role's policy never took effect"
as_role aws sts get-caller-identity --query Arn --output text > "${OUT}/role-identity.txt"
note "running plunge as $(cat "${OUT}/role-identity.txt")"

# refused <log>: print the line where AWS refused an SQS call, empty when none did.
# Only a refusal of an SQS action counts: a refusal of anything else (the record store, Cloud Control) would be a gap in the
# policy, not the tag condition working.
refused() { grep -m1 -iE 'sqs.*(AccessDenied|not authorized|StatusCode: 403)|(AccessDenied|not authorized).*sqs:' "$1" | sed -e 's/^[[:space:]]*//' -e "s/${ACCOUNT}/ACCOUNT/g" | cut -c1-400; }
role_pl() { as_role pl "$@"; }

# --- 1. create stack A's queue as the role: the request tag matches ---------------------------------------
note "1. up of stack A as the role (create, tag matches)"
role_pl a "${OUT}/a-create.log" up --yes; rc=$?
qurl_a="$(aws sqs get-queue-url --queue-name "${PFX}-a" --query QueueUrl --output text 2>/dev/null)"
aws sqs list-queue-tags --queue-url "${qurl_a:-none}" > "${OUT}/a-tags.json" 2>/dev/null
if [ ${rc} = 0 ] && grep -q "\"${VALUE_A}\"" "${OUT}/a-tags.json"; then
  verdict allow-create-a PASS "the role created stack A's queue; it carries plunge-stack=${VALUE_A}"
else
  verdict allow-create-a FAIL "exit ${rc}: $(refused "${OUT}/a-create.log")"
fi

# --- 2. update stack A's queue as the role -----------------------------------------------------------------
note "2. update of stack A's queue as the role"
pl a "${OUT}/setup.log" config set retention 7200
role_pl a "${OUT}/a-update.log" up --yes; rc=$?
got="$(aws sqs get-queue-attributes --queue-url "${qurl_a:-none}" --attribute-names MessageRetentionPeriod \
  --query Attributes.MessageRetentionPeriod --output text 2>/dev/null)"
if [ ${rc} = 0 ] && [ "${got}" = 7200 ]; then
  verdict allow-update-a PASS "the role changed stack A's queue retention to 7200"
else
  verdict allow-update-a FAIL "exit ${rc}, retention ${got}: $(refused "${OUT}/a-update.log")"
fi

# --- 3. update stack B's queue as the role -----------------------------------------------------------------
note "3. update of stack B's queue as the role"
before="$(retention_b)"
pl b "${OUT}/setup.log" config set retention 9000
role_pl b "${OUT}/b-update.log" up --yes; rc=$?
after="$(retention_b)"
why="$(refused "${OUT}/b-update.log")"
if [ ${rc} != 0 ] && [ -n "${why}" ] && [ "${before}" = "${after}" ]; then
  verdict refuse-update-b PASS "AWS refused the role's update of stack B's queue; retention stayed ${after}: ${why}"
else
  verdict refuse-update-b FAIL "exit ${rc}, retention ${before} -> ${after}, refusal line: ${why:-none}"
fi

# --- 4. destroy stack B as the role ------------------------------------------------------------------------
note "4. destroy of stack B as the role"
role_pl b "${OUT}/b-destroy.log" destroy --yes; rc=$?
why="$(refused "${OUT}/b-destroy.log")"
if aws sqs get-queue-url --queue-name "${PFX}-b" >/dev/null 2>&1 && [ ${rc} != 0 ] && [ -n "${why}" ]; then
  verdict refuse-destroy-b PASS "AWS refused the role's delete of stack B's queue; the queue still exists: ${why}"
else
  verdict refuse-destroy-b FAIL "exit ${rc}, refusal line: ${why:-none}"
fi

# --- 5. create under another stack's marker ----------------------------------------------------------------
note "5. up of stack C as the role (create, tag does not match)"
role_pl c "${OUT}/c-create.log" up --yes; rc=$?
why="$(refused "${OUT}/c-create.log")"
if [ ${rc} != 0 ] && [ -n "${why}" ] && ! aws sqs get-queue-url --queue-name "${PFX}-c" >/dev/null 2>&1; then
  verdict refuse-create-c PASS "AWS refused the role's create of a queue marked ${PROJECT}/${STACK_C}; no queue was made: ${why}"
else
  verdict refuse-create-c FAIL "exit ${rc}, refusal line: ${why:-none}"
fi

# --- 6. controls, straight to the API: the policy is what refuses, not plunge ------------------------------
note "6. direct API controls as the role"
as_role aws sqs create-queue --queue-name "${PFX}-d" > "${OUT}/control-untagged.log" 2>&1; rc=$?
as_role aws sqs create-queue --queue-name "${PFX}-d" --tags "plunge-stack=${VALUE_A}" > "${OUT}/control-tagged.log" 2>&1; rc2=$?
if [ ${rc} != 0 ] && [ -n "$(refused "${OUT}/control-untagged.log")" ] && [ ${rc2} = 0 ]; then
  verdict control-cli-create PASS "create-queue without the tag refused, with plunge-stack=${VALUE_A} allowed"
else
  verdict control-cli-create FAIL "untagged exit ${rc}, tagged exit ${rc2}"
fi
urld="$(aws sqs get-queue-url --queue-name "${PFX}-d" --query QueueUrl --output text 2>/dev/null)"
as_role aws sqs delete-queue --queue-url "${urld:-none}" > "${OUT}/control-delete-d.log" 2>&1
[ -n "${urld}" ] && aws sqs delete-queue --queue-url "${urld}" >/dev/null 2>&1

cp "${WORK}/a/Pulumi.yaml" "${OUT}/Pulumi.yaml" 2>/dev/null
cleanup
rm -rf "${WORK}"
echo "--- verdicts"; cat "${VERDICTS}"
! grep -q ' FAIL ' "${VERDICTS}"
