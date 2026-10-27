---
title: Azure
description: Two tags mark each resource. ARM lists them by tag.
prompt: |
  Read https://intentius.io/plunge/azure/ and tell me which of its pages applies to my task. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
Each taggable resource carries readable `plunge-stack` (`<project>/<stack>`) and `plunge-urn` tags. plunge covers pulumi-azure and azure-native.

- [Adopt](/plunge/azure/adopt/): bring a stock stack's resources onto records.
- [Gate](/plunge/azure/gate/): fence writes by the owning stack.
- [Operate](/plunge/azure/operate/): rename, remove, saved plans, two runs at once.
- [Compatibility](/plunge/azure/compatibility/): which providers and types take markers, and how they are listed.
- [Proof](/plunge/azure/proof/): the recorded run on floci-az, the Azure emulator, and the runs still to come.

Status: one preview is recorded on floci-az, and nothing has run on Azure itself.
