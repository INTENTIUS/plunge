---
title: GCP
description: Two labels mark each resource, as digests. Each service lists its own.
prompt: |
  Read https://intentius.io/plunge/gcp/ and tell me which of its pages applies to my task. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
Google label values are short and lower-case, so the `plunge-stack` and `plunge-urn` labels are hex digests: 16 characters for the stack, 32 for the URN. plunge covers pulumi-gcp.

- [Adopt](/plunge/gcp/adopt/): bring a stock stack's resources onto records.
- [Gate](/plunge/gcp/gate/): fence writes by the owning stack.
- [Operate](/plunge/gcp/operate/): rename, remove, saved plans, two runs at once.
- [Compatibility](/plunge/gcp/compatibility/): which providers and types take markers, and how they are listed.
- [Proof](/plunge/gcp/proof/): the recorded run on floci-gcp, the Google Cloud emulator.
