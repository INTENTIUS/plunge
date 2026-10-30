#!/usr/bin/env bash
#
# The concurrency races C1 to C6 on floci (INTENTIUS/plunge#7, the parity table on INTENTIUS/plunge#2), mirroring
# choudoufu's claim 2 (no-self-managed-locks, two-writers-one-record, cas-holds-under-every-sse-flavour), claim 5
# (recovery-is-a-rerun) and claim 15 (saved-plan refusal). The maintainer runs this by hand; it talks only to a
# local floci unless PLUNGE_RACES_TARGET=aws, which runs the same races on real AWS in us-east-1 (see lib.sh).
#
# Usage:
#   scripts/plunge-races/races.sh [race ...]
#
# Races: c1 c1-native c2 c3 c4 c5-disjoint c5-same c5-sse c6 (default: all, in that order), and lifecycle and
# readback, which run only when named. Together lifecycle and readback are the record store acceptance run
# (INTENTIUS/plunge#6), which had its own script until INTENTIUS/plunge#58. See lib.sh for the environment.
#
# Output in $OUT/<race>/: each run's log (a.log, b.log, ...), records.txt (the record listing after the race,
# decoded), records.json, and $OUT/verdicts.txt with one line per claim:
#   VERDICT <claim> <race> PASS|FAIL <why>
# Read the verdict lines, the logs and the record listing, not the exit code. The exit code is 1 when any verdict
# is FAIL.

source "$(dirname "$0")/lib.sh"

# both <cmd-a> <cmd-b>: run two shell commands at once and wait for both. Sets RC_A and RC_B.
both() {
  set +e
  bash -c "$1" &
  local pa=$!
  bash -c "$2" &
  local pb=$!
  wait "${pa}"
  RC_A=$?
  wait "${pb}"
  RC_B=$?
  set -e
}

# runner <dir> <log> <args...>: a command line for `both` running plunge in a checkout.
runner() {
  local dir="$1" log="$2"
  shift 2
  printf 'cd %q && PULUMI_BACKEND_URL=%q %q' "${dir}" "${BACKEND}" "${PLUNGE}"
  printf ' %q' "$@" --stack "${STACK}" --non-interactive
  printf ' >>%q 2>&1' "${log}"
}

# C1: two creates of one client-named resource. Two operators run the first `up` of one stack at once. The cloud's
# uniqueness constraint refuses the second create of the bucket and the role; the loser's re-run binds the
# winner's objects by marker (or plans against the winner's record) and creates nothing.
race_c1() {
  race_begin c1
  c1_race c1 site role "${PREFIX}-site" "${PREFIX}-app"
}

# C1 over aws-native types (INTENTIUS/plunge#36, for #10's aws-native leg): the same race with an
# aws-native:s3:Bucket (bucketName) and an aws-native:iam:Role (roleName), whose creates and the bind phase's
# listing go through Cloud Control on floci. The aws-native provider takes its endpoint from AWS_ENDPOINT_URL, so
# the race fails first if any log shows a real AWS endpoint.
race_c1_native() {
  race_begin c1-native
  c1_race c1-native native-site native-role "${PREFIX}-nsite" "${PREFIX}-napp"
}

# c1_race <race> <bucket-resource> <role-resource> <bucket-name> <role-name>: the C1 race body.
c1_race() {
  local race="$1" site="$2" role="$3" bucket="$4" rolename="$5"
  checkout "${WORK}/a" "${site}" "${role}"
  checkout "${WORK}/b" "${site}" "${role}"
  both "$(runner "${WORK}/a" "${WORK}/a.log" up --yes)" "$(runner "${WORK}/b" "${WORK}/b.log" up --yes)"
  echo "first runs: a=${RC_A} b=${RC_B}" | tee -a "${WORK}/race.txt"
  local d
  for d in a b; do
    pl "${WORK}/${d}" "${WORK}/${d}-rerun.log" up --yes || true
  done
  pl "${WORK}/a" "${WORK}/a-preview.log" preview --json || true
  pl "${WORK}/b" "${WORK}/b-preview.log" preview --json || true
  records
  real_cloud_rejections "${WORK}"/*.log > "${WORK}/real-cloud.txt"
  if [ -s "${WORK}/real-cloud.txt" ]; then
    verdict C1 "${race}" FAIL "a log names a real AWS endpoint or a rejection of the test keys; stop and see real-cloud.txt"
    return
  fi
  local buckets roles
  buckets="$(awsf s3api list-buckets --query "Buckets[?Name=='${bucket}'].Name" --output text | wc -w | tr -d ' ')"
  roles="$(awsf iam list-roles --query "Roles[?RoleName=='${rolename}'].RoleName" --output text | wc -w | tr -d ' ')"
  echo "live: ${buckets} bucket(s) ${bucket}, ${roles} role(s) ${rolename}" | tee -a "${WORK}/race.txt"
  local tags
  tags="$(awsf s3api get-bucket-tagging --bucket "${bucket}" 2>/dev/null || true)"
  echo "${tags}" > "${WORK}/site-tags.json"
  if [ "${buckets}" = 1 ] && [ "${roles}" = 1 ] && no_changes "${WORK}/a-preview.log" &&
    no_changes "${WORK}/b-preview.log" && grep -q plunge-urn "${WORK}/site-tags.json"; then
    verdict C1 "${race}" PASS "one bucket and one role after two racing first runs and their re-runs, marked, and both checkouts plan no changes"
  else
    verdict C1 "${race}" FAIL "buckets=${buckets} roles=${roles}; see a-preview.log, b-preview.log, site-tags.json"
  fi
}

# C2: two creates of a server-assigned resource. Two first runs create a VPC at once. Either the second run's bind
# phase found the first VPC by its markers (one VPC), or two VPCs carry the markers and the next run is refused
# naming both IDs (CollisionError). Two marked VPCs and no refusal is the failure.
race_c2() {
  race_begin c2
  checkout "${WORK}/a" net
  checkout "${WORK}/b" net
  both "$(runner "${WORK}/a" "${WORK}/a.log" up --yes)" "$(runner "${WORK}/b" "${WORK}/b.log" up --yes)"
  echo "first runs: a=${RC_A} b=${RC_B}" | tee -a "${WORK}/race.txt"
  local rc=0
  pl "${WORK}/a" "${WORK}/a-rerun.log" up --yes || rc=$?
  records
  if [ "${TARGET}" = aws ]; then
    # A real account has other VPCs; count only the ones marked for this race's stack.
    awsf ec2 describe-vpcs --filters "Name=cidr,Values=10.77.0.0/16" "Name=tag-key,Values=plunge-urn" \
      --query "Vpcs[?Tags[?Key=='plunge-stack' && Value=='plunge-races/${STACK}']].VpcId" --output text |
      tr '\t' '\n' | sed '/^$/d' | sort > "${WORK}/marked-vpcs.txt"
  else
    awsf ec2 describe-vpcs --filters "Name=cidr,Values=10.77.0.0/16" "Name=tag-key,Values=plunge-urn" \
      --query 'Vpcs[].VpcId' --output text | tr '\t' '\n' | sed '/^$/d' | sort > "${WORK}/marked-vpcs.txt"
  fi
  local n
  n="$(wc -l < "${WORK}/marked-vpcs.txt" | tr -d ' ')"
  echo "marked VPCs: ${n} ($(tr '\n' ' ' < "${WORK}/marked-vpcs.txt")); re-run exit ${rc}" | tee -a "${WORK}/race.txt"
  if [ "${n}" = 1 ]; then
    verdict C2 c2 PASS "one marked VPC: the racing create was bound by marker"
  elif [ "${n}" -ge 2 ] && grep -q "already carry its ownership markers" "${WORK}/a-rerun.log" &&
    names_all "${WORK}/marked-vpcs.txt" "${WORK}/a-rerun.log"; then
    verdict C2 c2 PASS "${n} marked VPCs and the next run is refused naming every one (CollisionError)"
  else
    verdict C2 c2 FAIL "${n} marked VPCs and the next run (exit ${rc}) did not refuse naming them; see a-rerun.log and records.txt"
  fi
}

# names_all <ids-file> <log>: every ID in the file appears in the log.
names_all() {
  local id
  while read -r id; do
    grep -qF "${id}" "$2" || return 1
  done < "$1"
}

queue_retention() {
  awsf sqs get-queue-attributes --queue-url "$(awsf sqs get-queue-url --queue-name "$1" --query QueueUrl --output text)" \
    --attribute-names MessageRetentionPeriod --query Attributes.MessageRetentionPeriod --output text
}

# C3, mirroring choudoufu claim 15: a saved plan is refused when a record it was computed against moved. Plan A is
# saved (retention 7200), run B lands (retention 1800), then `up --plan` of A must be refused with exit 40 naming
# the record, and the queue stays at B's value.
race_c3() {
  race_begin c3
  checkout "${WORK}/a" queue-a
  checkout "${WORK}/b" queue-a
  pl "${WORK}/a" "${WORK}/setup-up.log" up --yes || { verdict C3 c3 FAIL "the setup up failed; see setup-up.log"; return; }
  stack_config "${WORK}/a" retentionA=7200
  stack_config "${WORK}/b" retentionA=1800
  pl "${WORK}/a" "${WORK}/a-save.log" preview --save-plan "${WORK}/plan-a.json" ||
    { verdict C3 c3 FAIL "preview --save-plan failed; see a-save.log"; return; }
  pl "${WORK}/b" "${WORK}/b.log" up --yes || { verdict C3 c3 FAIL "run B failed; see b.log"; return; }
  local rc=0
  pl "${WORK}/a" "${WORK}/a-apply.log" up --yes --plan "${WORK}/plan-a.json" || rc=$?
  records
  local live
  live="$(queue_retention "${PREFIX}-a")"
  echo "up --plan exit ${rc}; live retention ${live}" | tee -a "${WORK}/race.txt"
  if [ "${rc}" = 40 ] && grep -q "queue-a" "${WORK}/a-apply.log" && [ "${live}" = 1800 ]; then
    verdict C3 c3 PASS "the moved saved plan is refused with exit 40 naming queue-a, and the queue keeps run B's 1800"
  else
    verdict C3 c3 FAIL "exit ${rc}, live retention ${live}; see a-apply.log"
  fi
  # The unmoved control: a fresh saved plan applies.
  local rc2=0
  pl "${WORK}/a" "${WORK}/a-save2.log" preview --save-plan "${WORK}/plan-a2.json" &&
    pl "${WORK}/a" "${WORK}/a-apply2.log" up --yes --plan "${WORK}/plan-a2.json" || rc2=$?
  if [ "${rc2}" = 0 ] && [ "$(queue_retention "${PREFIX}-a")" = 7200 ]; then
    verdict C3 c3-control PASS "an unmoved saved plan applies (retention 7200)"
  else
    verdict C3 c3-control FAIL "exit ${rc2}; see a-apply2.log"
  fi
}

# C4: an update racing a destroy. Checkout A no longer declares worker-1 (destroy); checkout B updates its
# retention. The loser is told by name (its record changed under it, or the object is gone) and its re-run plans
# against what is there now; nothing is planned from a stale view.
race_c4() {
  race_begin c4
  checkout "${WORK}/a" queue-a worker-1
  checkout "${WORK}/b" queue-a worker-1
  pl "${WORK}/a" "${WORK}/setup-up.log" up --yes || { verdict C4 c4 FAIL "the setup up failed; see setup-up.log"; return; }
  write_program "${WORK}/a" queue-a
  stack_config "${WORK}/b" retentionW=7200
  both "$(runner "${WORK}/a" "${WORK}/a.log" up --yes)" "$(runner "${WORK}/b" "${WORK}/b.log" up --yes)"
  echo "racing runs: a(destroy)=${RC_A} b(update)=${RC_B}" | tee -a "${WORK}/race.txt"
  local named=yes
  if [ "${RC_A}" != 0 ] && ! grep -qE "changed under this run|not found|does not exist" "${WORK}/a.log"; then named=no; fi
  if [ "${RC_B}" != 0 ] && ! grep -qE "changed under this run|not found|does not exist" "${WORK}/b.log"; then named=no; fi
  # Each loser re-runs once; then the last run's view is checked against the cloud.
  [ "${RC_A}" = 0 ] || pl "${WORK}/a" "${WORK}/a-rerun.log" up --yes || true
  [ "${RC_B}" = 0 ] || pl "${WORK}/b" "${WORK}/b-rerun.log" up --yes || true
  pl "${WORK}/a" "${WORK}/a-final.log" up --yes || true
  pl "${WORK}/a" "${WORK}/a-preview.log" preview --json || true
  records
  local live
  if [ "${TARGET}" = aws ]; then
    live="$(live_queues "${PREFIX}-w1")"
  else
    live="$(awsf sqs list-queues --queue-name-prefix "${PREFIX}-w1" --query 'QueueUrls' --output text 2>/dev/null | wc -w | tr -d ' ')"
  fi
  local in_records=no
  grep -q "::worker-1 " "${WORK}/records.txt" && in_records=yes
  echo "after convergence on A's program: live worker-1 queues ${live}, in records ${in_records}" | tee -a "${WORK}/race.txt"
  if [ "${RC_A}" = 0 ] || [ "${RC_B}" = 0 ]; then :; else named=no; fi
  if [ "${named}" = yes ] && [ "${live}" = 0 ] && [ "${in_records}" = no ] && no_changes "${WORK}/a-preview.log"; then
    verdict C4 c4 PASS "the losing run was told by name, and after its re-run the destroy holds: no worker-1 live or in the records, no changes planned"
  else
    verdict C4 c4 FAIL "loser named=${named}, live worker-1=${live}, in records=${in_records}; see a.log, b.log, a-preview.log"
  fi
}

# C5 disjoint (choudoufu #1938): two runs change different resources at once and both land.
race_c5_disjoint() {
  race_begin c5-disjoint
  checkout "${WORK}/a" queue-a queue-b
  checkout "${WORK}/b" queue-a queue-b
  pl "${WORK}/a" "${WORK}/setup-up.log" up --yes || { verdict C5 c5-disjoint FAIL "the setup up failed"; return; }
  # Each checkout changes only its own queue; each carries the other's value as it was.
  stack_config "${WORK}/a" retentionA=7200
  stack_config "${WORK}/b" retentionB=1800
  both "$(runner "${WORK}/a" "${WORK}/a.log" up --yes)" "$(runner "${WORK}/b" "${WORK}/b.log" up --yes)"
  records
  local ra rb
  ra="$(queue_retention "${PREFIX}-a")"
  rb="$(queue_retention "${PREFIX}-b")"
  echo "exits a=${RC_A} b=${RC_B}; live a=${ra} b=${rb}" | tee -a "${WORK}/race.txt"
  if [ "${RC_A}" = 0 ] && [ "${RC_B}" = 0 ] && [ "${ra}" = 7200 ] && [ "${rb}" = 1800 ]; then
    verdict C5 c5-disjoint PASS "two disjoint applies both land with no lock (a=7200, b=1800)"
  else
    verdict C5 c5-disjoint FAIL "exits a=${RC_A} b=${RC_B}, live a=${ra} b=${rb}; see a.log, b.log"
  fi
}

# same_record_race <race> <claim-detail>: two runs change queue-a's record at once. Mirrors choudoufu's
# two-writers-one-record and no-self-managed-locks: exactly one record write lands, the other run is told by name
# (RecordConflictError, naming queue-a), and no lock object appears in the store at any point.
same_record_race() {
  local race="$1"
  checkout "${WORK}/a" queue-a
  checkout "${WORK}/b" queue-a
  pl "${WORK}/a" "${WORK}/setup-up.log" up --yes || { verdict C5 "${race}" FAIL "the setup up failed"; return; }
  stack_config "${WORK}/a" retentionA=7200
  stack_config "${WORK}/b" retentionA=1800
  : > "${WORK}/locks-seen.txt"
  (
    for _ in $(seq 1 120); do
      lockish_keys >> "${WORK}/locks-seen.txt"
      sleep 0.5
    done
  ) &
  local watcher=$!
  both "$(runner "${WORK}/a" "${WORK}/a.log" up --yes)" "$(runner "${WORK}/b" "${WORK}/b.log" up --yes)"
  kill "${watcher}" 2>/dev/null || true
  wait "${watcher}" 2>/dev/null || true
  records
  local rec live
  rec="$(record_of queue-a | python3 -I -c 'import json,sys
d=json.load(sys.stdin)
s=(d.get("states") or d.get("States") or [{}])[0]
print((s.get("inputs") or s.get("Inputs") or {}).get("messageRetentionSeconds"))' 2>/dev/null || echo unreadable)"
  live="$(queue_retention "${PREFIX}-a")"
  local locks
  locks="$(sort -u "${WORK}/locks-seen.txt" | sed '/^$/d' | wc -l | tr -d ' ')"
  echo "exits a=${RC_A} b=${RC_B}; record retention ${rec}; live ${live}; lock-like keys seen ${locks}" | tee -a "${WORK}/race.txt"
  local loser_named=no
  if { [ "${RC_A}" = 0 ] && [ "${RC_B}" != 0 ] && grep -q "changed under this run" "${WORK}/b.log" && grep -q queue-a "${WORK}/b.log"; } ||
    { [ "${RC_B}" = 0 ] && [ "${RC_A}" != 0 ] && grep -q "changed under this run" "${WORK}/a.log" && grep -q queue-a "${WORK}/a.log"; }; then
    loser_named=yes
  fi
  if [ "${loser_named}" = yes ]; then
    verdict C5 "${race}" PASS "one write landed (record retention ${rec}); the other run was refused by name for queue-a's record"
  elif [ "${RC_A}" = 0 ] && [ "${RC_B}" = 0 ]; then
    # Both may land when the runs did not overlap; that is not a race, and the run says so rather than passing.
    verdict C5 "${race}" FAIL "both runs succeeded, so the writes did not overlap; re-run this race (record ${rec}, live ${live})"
  else
    verdict C5 "${race}" FAIL "exits a=${RC_A} b=${RC_B} and no run was refused by name; see a.log, b.log"
  fi
  if [ "${locks}" = 0 ]; then
    verdict C5 "${race}-no-locks" PASS "no lock object appeared in the record store during the race"
  else
    verdict C5 "${race}-no-locks" FAIL "lock-like keys appeared: $(sort -u "${WORK}/locks-seen.txt" | tr '\n' ' ')"
  fi
}

# The minimal estate end to end (INTENTIUS/plunge#6's maintainer run): two queues in one stack on a plunge+s3://
# record store on floci. preview plans the two creates; up creates them; two checkouts then each change one queue at
# once, and both land with no lock object in the store; the export assembles (it passes VerifyIntegrity or fails)
# and holds both changes, with one resource record per URN; a run stamped with an older program generation that no
# longer declares queue-b keeps it and warns with both stamps; a tag changed through floci's API shows as drift on
# queue-a and queue-b reads in sync, each with one provider Read; destroy removes both queues and their records.
race_lifecycle() {
  race_begin lifecycle
  local urn_a="urn:pulumi:${STACK}::plunge-races::aws:sqs/queue:Queue::queue-a"
  local urn_b="urn:pulumi:${STACK}::plunge-races::aws:sqs/queue:Queue::queue-b"
  # Explicit stamps, so the result does not depend on where OUT sits relative to a git checkout.
  export PLUNGE_PROGRAM_COMMIT=newer PLUNGE_PROGRAM_TIME=2000000000
  checkout "${WORK}/a" queue-a queue-b
  checkout "${WORK}/b" queue-a queue-b

  # preview: two creates.
  pl "${WORK}/a" "${WORK}/preview.log" preview --json || { verdict L lifecycle-preview FAIL "preview failed; see preview.log"; return; }
  local creates
  creates="$(python3 -I - "${WORK}/preview.log" <<'PY2'
import json, sys
raw = open(sys.argv[1]).read()
doc = json.loads(raw[raw.find("{"):])
print(sum(1 for s in doc.get("steps") or [] if s.get("op") == "create" and s.get("urn", "").endswith(("::queue-a", "::queue-b"))))
PY2
)"
  if [ "${creates}" = 2 ]; then
    verdict L lifecycle-preview PASS "preview plans the two queue creates"
  else
    verdict L lifecycle-preview FAIL "preview planned ${creates} queue creates; see preview.log"
  fi

  # up: both queues, marked.
  pl "${WORK}/a" "${WORK}/up.log" up --yes || { verdict L lifecycle-up FAIL "up failed; see up.log"; return; }
  local tags
  tags="$(awsf sqs list-queue-tags --queue-url "$(awsf sqs get-queue-url --queue-name "${PREFIX}-a" --query QueueUrl --output text)")"
  echo "${tags}" > "${WORK}/queue-a-tags.json"
  if grep -q plunge-urn "${WORK}/queue-a-tags.json" && [ "$(queue_retention "${PREFIX}-b")" = 3600 ]; then
    verdict L lifecycle-up PASS "up created both queues, and queue-a carries its plunge-urn marker"
  else
    verdict L lifecycle-up FAIL "see up.log and queue-a-tags.json"
  fi

  # Two disjoint updates at once, with the store watched for lock objects.
  stack_config "${WORK}/a" retentionA=7200
  stack_config "${WORK}/b" retentionB=1800
  : > "${WORK}/locks-seen.txt"
  (
    for _ in $(seq 1 120); do
      lockish_keys >> "${WORK}/locks-seen.txt"
      sleep 0.5
    done
  ) &
  local watcher=$!
  both "$(runner "${WORK}/a" "${WORK}/a.log" up --yes)" "$(runner "${WORK}/b" "${WORK}/b.log" up --yes)"
  kill "${watcher}" 2>/dev/null || true
  wait "${watcher}" 2>/dev/null || true
  local ra rb locks
  ra="$(queue_retention "${PREFIX}-a")"
  rb="$(queue_retention "${PREFIX}-b")"
  locks="$(sort -u "${WORK}/locks-seen.txt" | sed '/^$/d' | wc -l | tr -d ' ')"
  echo "concurrent updates: exits a=${RC_A} b=${RC_B}; live a=${ra} b=${rb}; lock-like keys seen ${locks}" | tee -a "${WORK}/race.txt"
  if [ "${RC_A}" = 0 ] && [ "${RC_B}" = 0 ] && [ "${ra}" = 7200 ] && [ "${rb}" = 1800 ] && [ "${locks}" = 0 ]; then
    verdict C5 lifecycle-disjoint PASS "two concurrent updates on disjoint resources both land (a=7200, b=1800), no lock object seen"
  else
    verdict C5 lifecycle-disjoint FAIL "exits a=${RC_A} b=${RC_B}, live a=${ra} b=${rb}, lock-like keys ${locks}; see a.log, b.log"
  fi

  # The export assembles from the records, so it passed VerifyIntegrity; it holds both changes, one record per URN.
  records
  local xrc=0
  (cd "${WORK}/a" && PULUMI_BACKEND_URL="${BACKEND}" "${PLUNGE}" stack export --stack "${STACK}" --non-interactive) \
    > "${WORK}/export.json" 2> "${WORK}/export.stderr" || xrc=$?
  local xcheck
  xcheck="$(python3 -I - "${WORK}/export.json" "${WORK}/records.txt" <<'PY2'
import json, sys
dep = json.load(open(sys.argv[1]))["deployment"]
res = {r["urn"]: r for r in dep.get("resources") or []}
def ret(name):
  for u, r in res.items():
    if u.endswith("::" + name):
      return (r.get("inputs") or {}).get("messageRetentionSeconds")
recs = [l.split(" ")[1] for l in open(sys.argv[2]) if l.startswith("resources ")]
one_per_urn = len(recs) == len(set(recs)) and set(recs) == set(res)
print(f"a={ret('queue-a')} b={ret('queue-b')} resources={len(res)} records={len(recs)} one-per-urn={'yes' if one_per_urn else 'no'}")
PY2
)" || xcheck="unreadable"
  echo "export: exit ${xrc}; ${xcheck}" | tee -a "${WORK}/race.txt"
  if [ "${xrc}" = 0 ] && [ "${xcheck#a=7200 b=1800 }" != "${xcheck}" ] && [ "${xcheck%one-per-urn=yes}" != "${xcheck}" ]; then
    verdict P6 lifecycle-export PASS "the export assembles with integrity checking on, holds a=7200 and b=1800, and the store has one resource record per URN"
  else
    verdict P6 lifecycle-export FAIL "export exit ${xrc}; ${xcheck}; see export.stderr and records.txt"
  fi

  # A run of an older program that no longer declares queue-b keeps it at its value, warning with both stamps.
  checkout "${WORK}/c" queue-a
  stack_config "${WORK}/c" retentionA=7200
  local orc=0
  PLUNGE_PROGRAM_COMMIT=older PLUNGE_PROGRAM_TIME=1000000000 pl "${WORK}/c" "${WORK}/older.log" up --yes || orc=$?
  local rb2
  rb2="$(queue_retention "${PREFIX}-b" 2>/dev/null || echo gone)"
  echo "older run: exit ${orc}; live b=${rb2}" | tee -a "${WORK}/race.txt"
  if [ "${orc}" = 0 ] && [ "${rb2}" = "${rb}" ] && grep -q queue-b "${WORK}/older.log" && grep -q newer "${WORK}/older.log" &&
    grep -q older "${WORK}/older.log"; then
    verdict P6 lifecycle-older-run PASS "an older program's run keeps queue-b and warns naming it and both stamps"
  else
    verdict P6 lifecycle-older-run FAIL "exit ${orc}, live b=${rb2}; see older.log"
  fi

  # Drift: change queue-a's race tag through floci's API, then read each queue alone.
  awsf sqs tag-queue --queue-url "$(awsf sqs get-queue-url --queue-name "${PREFIX}-a" --query QueueUrl --output text)" \
    --tags race=drifted
  local drc=0
  (cd "${WORK}/a" && PULUMI_BACKEND_URL="${BACKEND}" "${PLUNGE}" drift --resource "${urn_a}" --stream --stack "${STACK}" \
    --non-interactive) > "${WORK}/drift-a.ndjson" 2> "${WORK}/drift-a.stderr" || drc=$?
  (cd "${WORK}/a" && PULUMI_BACKEND_URL="${BACKEND}" "${PLUNGE}" drift --resource "${urn_b}" --stream --stack "${STACK}" \
    --non-interactive) > "${WORK}/drift-b.ndjson" 2> "${WORK}/drift-b.stderr" || drc=$((drc + $?))
  local dcheck
  dcheck="$(python3 -I - "${WORK}/drift-a.ndjson" "${WORK}/drift-b.ndjson" <<'PY2'
import json, sys
def lines(p):
  return [json.loads(l) for l in open(p) if l.strip()]
out = []
for p in sys.argv[1:]:
  ls = lines(p)
  rec = next((l for l in ls if l.get("kind") == "drift-record" or l.get("type") == "drift-record"), {})
  settle = next((l for l in ls if l.get("kind") == "drift-settle" or l.get("type") == "drift-settle"), {})
  out.append(f"{rec.get('status') or rec.get('state')}/{json.dumps(settle.get('reads'), sort_keys=True)}")
print(" ".join(out))
PY2
)" || dcheck="unreadable"
  echo "drift: exits ${drc}; ${dcheck}" | tee -a "${WORK}/race.txt"
  case "${dcheck}" in
    drifted/*" "in-sync/*)
      verdict L lifecycle-drift PASS "drift --resource reports queue-a drifted and queue-b in sync (${dcheck})" ;;
    *)
      verdict L lifecycle-drift FAIL "${dcheck}; see drift-a.ndjson, drift-b.ndjson" ;;
  esac

  # destroy: both queues and their records go.
  local zrc=0
  pl "${WORK}/a" "${WORK}/destroy.log" destroy --yes || zrc=$?
  records
  cp "${WORK}/records.txt" "${WORK}/records-after-destroy.txt"
  local live left
  if [ "${TARGET}" = aws ]; then
    live="$(live_queues "${PREFIX}-")"
  else
    live="$(awsf sqs list-queues --queue-name-prefix "${PREFIX}-" --query 'QueueUrls' --output text 2>/dev/null | sed 's/None//' | wc -w | tr -d ' ')"
  fi
  left="$(grep -c '^resources .*::queue-' "${WORK}/records-after-destroy.txt" || true)"
  echo "destroy: exit ${zrc}; live queues ${live}; queue records left ${left}" | tee -a "${WORK}/race.txt"
  if [ "${zrc}" = 0 ] && [ "${live}" = 0 ] && [ "${left}" = 0 ]; then
    verdict L lifecycle-destroy PASS "destroy removed both queues and their records"
  else
    verdict L lifecycle-destroy FAIL "exit ${zrc}, live queues ${live}, queue records ${left}; see destroy.log"
  fi

  real_cloud_rejections "${WORK}"/*.log "${WORK}"/*.stderr > "${WORK}/real-cloud.txt"
  if [ -s "${WORK}/real-cloud.txt" ]; then
    verdict L lifecycle-offline FAIL "a log names a real AWS endpoint or a rejection of the test keys; stop and see real-cloud.txt"
  fi
  unset PLUNGE_PROGRAM_COMMIT PLUNGE_PROGRAM_TIME
}

# The record store acceptance run's own program (INTENTIUS/plunge#6; its script was retired by INTENTIUS/plunge#58),
# on the race harness: two pulumi-random RandomStrings, rand-x and rand-y, whose keepers come
# from config. pulumi-random's Read hands a keeper back as a string where the program gives it in its own form, so
# every refresh reads back inputs that differ from the record only in form and modification time. That read is the
# same resource and must not be written (persister.go sameRead, 1a72ec8d30). A plain re-run then adds no version to
# either record; two concurrent runs, each changing one keeper, both land with neither refused for the other's
# record; and the export holds both keepers, compared by value since a record keeps whichever form its last write
# had. The store has one resource record per URN.
race_readback() {
  race_begin readback
  export PLUNGE_PROGRAM_COMMIT=newer PLUNGE_PROGRAM_TIME=2000000000
  checkout "${WORK}/a" rand-x rand-y
  checkout "${WORK}/b" rand-x rand-y
  pl "${WORK}/a" "${WORK}/setup-up.log" up --yes || { verdict P6 readback-setup FAIL "the setup up failed; see setup-up.log"; return; }

  # A plain re-run refreshes both RandomStrings and changes nothing: neither record gains a version.
  local before after
  before="$(record_versions rand-x rand-y | tr '\n' ' ')"
  pl "${WORK}/a" "${WORK}/rerun.log" up --yes || true
  after="$(record_versions rand-x rand-y | tr '\n' ' ')"
  echo "record versions: before re-run ${before}; after ${after}" | tee -a "${WORK}/race.txt"
  local written=yes
  case " ${before}" in *" 0 "*) written=no ;; esac
  if [ "${written}" = yes ] && [ "${before}" = "${after}" ]; then
    verdict P6 readback-no-rewrite PASS "a re-run whose refresh reads the keepers back as strings writes neither record (${after% })"
  else
    verdict P6 readback-no-rewrite FAIL "record versions before ${before:-none}, after ${after:-none}; see rerun.log"
  fi

  # Two runs at once, each changing one keeper, set as the acceptance script set it (`config set`).
  pl "${WORK}/a" "${WORK}/config.log" config set keeperX 2 || { verdict C5 readback-disjoint FAIL "config set failed; see config.log"; return; }
  pl "${WORK}/b" "${WORK}/config.log" config set keeperY 2 || { verdict C5 readback-disjoint FAIL "config set failed; see config.log"; return; }
  both "$(runner "${WORK}/a" "${WORK}/a.log" up --yes)" "$(runner "${WORK}/b" "${WORK}/b.log" up --yes)"
  local refused=no
  grep -q "changed under this run" "${WORK}/a.log" "${WORK}/b.log" && refused=yes
  records
  local xrc=0
  (cd "${WORK}/a" && PULUMI_BACKEND_URL="${BACKEND}" "${PLUNGE}" stack export --stack "${STACK}" --non-interactive) \
    > "${WORK}/export.json" 2> "${WORK}/export.stderr" || xrc=$?
  local xcheck
  xcheck="$(python3 -I - "${WORK}/export.json" "${WORK}/records.txt" <<'PY2'
import json, sys
dep = json.load(open(sys.argv[1]))["deployment"]
res = {r["urn"]: r for r in dep.get("resources") or [] if not r.get("delete")}
def keeper(name):
  for u, r in res.items():
    if u.endswith("::" + name):
      return ((r.get("inputs") or {}).get("keepers") or {}).get("v")
# The record holds the keeper in whichever form its last write had, so the value is compared, not its JSON type.
ok = str(keeper("rand-x")) == "2" and str(keeper("rand-y")) == "2"
recs = [l.split(" ")[1] for l in open(sys.argv[2]) if l.startswith("resources ")]
one_per_urn = len(recs) == len(set(recs)) and set(recs) == set(res)
print(f"x={keeper('rand-x')!r} y={keeper('rand-y')!r} keepers={'yes' if ok else 'no'} resources={len(res)} records={len(recs)} one-per-urn={'yes' if one_per_urn else 'no'}")
PY2
)" || xcheck="unreadable"
  echo "concurrent runs: exits a=${RC_A} b=${RC_B}; refused=${refused}; export exit ${xrc}; ${xcheck}" | tee -a "${WORK}/race.txt"
  if [ "${RC_A}" = 0 ] && [ "${RC_B}" = 0 ] && [ "${refused}" = no ] && [ "${xrc}" = 0 ] &&
    [ "${xcheck#*keepers=yes}" != "${xcheck}" ]; then
    verdict C5 readback-disjoint PASS "two concurrent runs, each changing one RandomString's keeper, both land and the export holds both (${xcheck%% resources=*})"
  else
    verdict C5 readback-disjoint FAIL "exits a=${RC_A} b=${RC_B}, refused=${refused}, export exit ${xrc}; ${xcheck}; see a.log, b.log"
  fi
  if [ "${xrc}" = 0 ] && [ "${xcheck%one-per-urn=yes}" != "${xcheck}" ]; then
    verdict P6 readback-export PASS "the export assembles with integrity checking on and the store has one resource record per URN"
  else
    verdict P6 readback-export FAIL "export exit ${xrc}; ${xcheck}; see export.stderr and records.txt"
  fi

  real_cloud_rejections "${WORK}"/*.log "${WORK}"/*.stderr > "${WORK}/real-cloud.txt"
  if [ -s "${WORK}/real-cloud.txt" ]; then
    verdict P6 readback-offline FAIL "a log names a real AWS endpoint or a rejection of the test keys; stop and see real-cloud.txt"
  fi
  unset PLUNGE_PROGRAM_COMMIT PLUNGE_PROGRAM_TIME
}

race_c5_same() {
  race_begin c5-same
  same_record_race c5-same
}

# C5 under every SSE flavour floci serves (choudoufu's cas-holds-under-every-sse-flavour): the same-record race
# with the record bucket's default encryption set to SSE-S3 and to SSE-KMS.
race_c5_sse() {
  local flavour
  for flavour in sse-s3 sse-kms; do
    race_begin "c5-${flavour}"
    case "${flavour}" in
      sse-s3)
        awsf s3api put-bucket-encryption --bucket "${BUCKET}" --server-side-encryption-configuration \
          '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}' >/dev/null
        ;;
      sse-kms)
        if [ "${TARGET}" = aws ]; then
          # No customer key is created on real AWS: aws:kms with no KMSMasterKeyID uses the AWS-managed aws/s3 key.
          awsf s3api put-bucket-encryption --bucket "${BUCKET}" --server-side-encryption-configuration \
            '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"aws:kms"}}]}' >/dev/null
        else
          local key
          key="$(awsf kms create-key --query KeyMetadata.KeyId --output text)"
          awsf s3api put-bucket-encryption --bucket "${BUCKET}" --server-side-encryption-configuration \
            "{\"Rules\":[{\"ApplyServerSideEncryptionByDefault\":{\"SSEAlgorithm\":\"aws:kms\",\"KMSMasterKeyID\":\"${key}\"}}]}" >/dev/null
        fi
        ;;
    esac
    awsf s3api get-bucket-encryption --bucket "${BUCKET}" > "${WORK}/encryption.json"
    same_record_race "c5-${flavour}"
  done
}

# C6, mirroring choudoufu claim 5 (recovery-is-a-rerun): a run killed part way leaves nothing held. The re-run
# needs no cancel and no lock removal, finishes, and leaves each queue once.
race_c6() {
  race_begin c6
  checkout "${WORK}/a" q-0 q-1 q-2 q-3 q-4 q-5
  set +e
  (cd "${WORK}/a" && PULUMI_BACKEND_URL="${BACKEND}" exec "${PLUNGE}" up --yes --stack "${STACK}" --non-interactive) \
    > "${WORK}/a-killed.log" 2>&1 &
  local pid=$!
  local i
  for i in $(seq 1 600); do
    grep -qE "created|creating" "${WORK}/a-killed.log" 2>/dev/null && break
    kill -0 "${pid}" 2>/dev/null || break
    sleep 0.1
  done
  kill -9 "${pid}" 2>/dev/null
  wait "${pid}" 2>/dev/null
  set -e
  records
  cp "${WORK}/records.txt" "${WORK}/records-after-kill.txt"
  local rc=0
  pl "${WORK}/a" "${WORK}/a-rerun.log" up --yes || rc=$?
  pl "${WORK}/a" "${WORK}/a-preview.log" preview --json || true
  records
  local queues
  queues="$(awsf sqs list-queues --queue-name-prefix "${PREFIX}-q" --query 'QueueUrls' --output text 2>/dev/null | wc -w | tr -d ' ')"
  echo "killed after ${i} polls; re-run exit ${rc}; live queues ${queues}" | tee -a "${WORK}/race.txt"
  if [ "${rc}" = 0 ] && [ "${queues}" = 6 ] && ! grep -qiE "pending operation|locked|pulumi cancel" "${WORK}/a-rerun.log" &&
    no_changes "${WORK}/a-preview.log"; then
    verdict C6 c6 PASS "after kill -9 mid-run the plain re-run finished with no cancel or unlock, six queues each once, no changes planned"
  else
    verdict C6 c6 FAIL "re-run exit ${rc}, live queues ${queues}; see a-rerun.log and records-after-kill.txt"
  fi
}

if [ "${TARGET}" = aws ]; then
  trap 'aws_cleanup_all' EXIT
fi
ALL=(c1 c1-native c2 c3 c4 c5-disjoint c5-same c5-sse c6)
if [ "$#" -gt 0 ]; then RACES=("$@"); else RACES=("${ALL[@]}"); fi
for r in "${RACES[@]}"; do
  case "${r}" in
    c1) race_c1 ;;
    c1-native) race_c1_native ;;
    c2) race_c2 ;;
    c3) race_c3 ;;
    c4) race_c4 ;;
    c5-disjoint) race_c5_disjoint ;;
    c5-same) race_c5_same ;;
    c5-sse) race_c5_sse ;;
    c6) race_c6 ;;
    lifecycle) race_lifecycle ;;
    readback) race_readback ;;
    *) die "unknown race ${r} (races: ${ALL[*]})" ;;
  esac
  aws_cleanup_all
done
aws_leftovers

echo "results in ${OUT}"
if grep -q ' FAIL ' "${VERDICTS}"; then
  exit 1
fi
