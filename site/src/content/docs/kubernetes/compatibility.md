---
title: Compatibility
description: Which Kubernetes objects take markers and how they are listed.
prompt: |
  Follow https://intentius.io/plunge/kubernetes/compatibility/. List the Kubernetes kinds in this program and say for each whether it takes markers. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
| | pulumi-kubernetes |
|---|---|
| Markers | `metadata.labels`, digests, on every object kind |
| Left unmarked | `kubernetes:helm.sh/*`, `kubernetes:yaml/*`, `kubernetes:kustomize/*` |
| Names (C1) | not covered: no autoname schema is read for `metadata.name` |
| Listing (C2) | a LIST of the kind by label selector, collision claim made |
| Record store | `plunge+k8s://`, one Secret per record |

Source: `pkg/backend/plunge/catalog.go`, choudoufu's `pkg/kubesweep`.
