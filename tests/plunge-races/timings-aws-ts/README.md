# plunge up --json timings on aws-ts (INTENTIUS/plunge#55)

Recorded on 2026-10-28 on the pinned floci (`versions.txt`), plunge built from
main ab9158fdf0. `run.sh` made a versioned `plunge+s3://` record store on floci,
ran `plunge stack init floci` and `plunge up --yes --json --stack floci` on a
copy of `tests/plunge-corpus/aws-ts` (after `npm ci`), and exported the stack.
It ran under the safe-run wrapper:

```sh
scripts/plunge-safe-run.sh --log <out>/safe-run.log --scan <out> -e PATH=<plunge bin dir>:$PATH -- \
  tests/plunge-races/timings-aws-ts/run.sh <aws-ts copy> <out> <plunge binary>
python3 -I tests/plunge-races/timings-aws-ts/check.py <out>/up.json > <out>/timings-check.txt
```

`up.json` is the command's stdout as written. `timings-check.txt` lists
`metadata.plungeTimings` per resource event and ends in a verdict line. It
reads FAIL: the 15 custom and provider resources carry their provider calls
and record writes, and the component `docs` and the stack resource carry no
timings, though the store holds records for both.
