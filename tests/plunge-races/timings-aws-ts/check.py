# Summarises metadata.plungeTimings on the resource events of a `plunge up --json` output (INTENTIUS/plunge#55).
import json, sys, collections
raw = open(sys.argv[1]).read(); dec = json.JSONDecoder(); i = 0; docs = []
while True:
    while i < len(raw) and raw[i].isspace(): i += 1
    if i >= len(raw): break
    d, i = dec.raw_decode(raw, i); docs.append(d)
kinds = collections.Counter(k for d in docs for k in d if k.endswith("Event"))
print("documents", len(docs), "events", dict(kinds))
custom_out = 0; missing = []; calls = collections.Counter(); bad = []; carried = []
for d in docs:
    for key in ("resOutputsEvent", "resOpFailedEvent"):
        e = d.get(key)
        if not e: continue
        md = e.get("metadata") or {}
        urn = md.get("urn", "")
        t = md.get("plungeTimings")
        typ = md.get("type", "")
        changed = md.get("op") != "same"
        if key == "resOutputsEvent" and changed: custom_out += 1
        if not t:
            if changed: missing.append(f"{key} {md.get('op')} {typ} {urn.rsplit('::',1)[-1]}")
            continue
        for x in t:
            calls[(x["kind"], x["call"])] += 1
            if x.get("urn"): carried.append(f"{x['urn'].rsplit('::',1)[-1]} {x['kind']}:{x['call']} on {urn.rsplit('::',1)[-1]}")
            if not isinstance(x.get("durationMs"), (int, float)) or x["durationMs"] < 0 or not x.get("startedAt"):
                bad.append(f"{urn} {x}")
        print(f"{key} {md.get('op'):8} {urn.rsplit('::',1)[-1]:22} " + ", ".join((f"[{x['urn'].rsplit('::',1)[-1]}] " if x.get('urn') else "") + f"{x['kind']}:{x['call']} {x['durationMs']:.1f}ms" + (" failed" if x.get('failed') else "") for x in t))
print("calls", {f"{k}:{c}": n for (k, c), n in sorted(calls.items())})
print("changed resources with an outputs event:", custom_out)
print("changed resource events without plungeTimings:", missing or "none")
print("entries without a duration or start time:", bad or "none")
print("calls carried for another resource (a default provider's, on its first user's event):", carried or "none")
ok = custom_out > 0 and not missing and not bad and calls[("record", "write")] > 0 and any(k == "provider" for k, _ in calls)
print("VERDICT plunge-timings", "PASS" if ok else "FAIL")
