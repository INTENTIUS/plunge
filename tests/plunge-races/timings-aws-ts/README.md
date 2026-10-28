# plunge up --json timings on aws-ts (INTENTIUS/plunge#55)

Recorded on 2026-10-28 on the pinned floci (`versions.txt`), plunge built from
3654404be6, the fix for the component, stack and default provider writes.
`run.sh` made a versioned `plunge+s3://` record store on floci, ran
`plunge stack init floci` and `plunge up --yes --json --stack floci` on a copy
of `tests/plunge-corpus/aws-ts` (after `npm ci`, with the floci port in
`Pulumi.floci.yaml` set to the one `FLOCI` names), and exported the stack. It
ran under the safe-run wrapper:

```sh
scripts/plunge-safe-run.sh --log <out>/safe-run.log --scan <out> -e PATH=<plunge bin dir>:$PATH \
  -e FLOCI=http://localhost:4576 -- \
  tests/plunge-races/timings-aws-ts/run.sh <aws-ts copy> <out> <plunge binary>
python3 -I tests/plunge-races/timings-aws-ts/check.py <out>/up.json > <out>/timings-check.txt
```

`up.json` is the command's stdout as written. `timings-check.txt` lists
`metadata.plungeTimings` per resource event and ends in a verdict line, which
reads PASS: every changed resource's outputs event carries timings, the
component `docs` and the stack resource included. The default provider's
step event is internal and `up --json` leaves it out, so its record write is
on the first event of a resource that uses it (`db-password`), with the
provider's URN in the entry's `urn` field. `record-versions.txt` compares each
resource record's S3 version count with the record writes in `up.json`: 39
and 39, one for one.
