#!/usr/bin/env bash
# plunge#55 evidence: `plunge up --json` of tests/plunge-corpus/aws-ts on floci, on a plunge+s3:// store on floci.
# Run under scripts/plunge-safe-run.sh. Args: <program dir> <out dir> <plunge binary>
set -euo pipefail
P="$1"; O="$2"; PLUNGE="$3"
FLOCI="${FLOCI:-http://localhost:4566}"  # the program's Pulumi.floci.yaml must name the same port
B=cp-rec-p55-records
mkdir -p "$O"
awsf() { aws --endpoint-url "$FLOCI" --output json "$@"; }
awsf s3api create-bucket --bucket "$B" >/dev/null 2>&1 || true
awsf s3api put-bucket-versioning --bucket "$B" --versioning-configuration Status=Enabled
awsf s3api put-bucket-lifecycle-configuration --bucket "$B" --lifecycle-configuration \
  '{"Rules":[{"ID":"records-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' >/dev/null
awsf s3api put-public-access-block --bucket "$B" --public-access-block-configuration \
  BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
export PULUMI_BACKEND_URL="plunge+s3://$B/aws-ts-$(date -u +%Y%m%dT%H%M%SZ)?endpoint=$FLOCI&s3ForcePathStyle=true&region=us-east-1"
export PULUMI_CONFIG_PASSPHRASE=plunge-baseline
echo "$PULUMI_BACKEND_URL" > "$O/store-url.txt"
cd "$P"
"$PLUNGE" stack init floci --secrets-provider passphrase > "$O/init.txt" 2>&1
set +e
"$PLUNGE" up --yes --json --stack floci > "$O/up.json" 2> "$O/up.stderr"
echo "$?" > "$O/up.exit.txt"
set -e
"$PLUNGE" stack export --stack floci --file "$O/export.json" > /dev/null 2>&1 || true
