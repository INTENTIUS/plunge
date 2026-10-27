---
title: AWS
description: Two tags mark each resource. Cloud Control lists them before a create.
prompt: |
  Read https://intentius.io/plunge/aws/ and tell me which of its pages applies to my task. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
The create call writes `plunge-stack` (`<project>/<stack>`) and `plunge-urn` tags on each taggable resource, for pulumi-aws and aws-native.

- [Adopt](/plunge/aws/adopt/): bring a stock stack's resources onto records.
- [Gate](/plunge/aws/gate/): fence writes by the owning stack.
- [Operate](/plunge/aws/operate/): rename, remove, saved plans, two runs at once.
- [Compatibility](/plunge/aws/compatibility/): which providers and types take markers, and how they are listed.
- [Proof](/plunge/aws/proof/): recorded runs on floci, the AWS emulator.
