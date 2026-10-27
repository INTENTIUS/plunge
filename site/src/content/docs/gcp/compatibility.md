---
title: Compatibility
description: Which Google Cloud types take markers, which plunge names, and how each is listed.
prompt: |
  Follow https://intentius.io/plunge/gcp/compatibility/. List the GCP resource types in this program and say for each whether it takes labels and how it is listed. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
| | pulumi-gcp |
|---|---|
| Markers | map `labels`, from the bridge's Terraform schema, digests |
| Names (C1) | where the bridge marks a field autonamed, it forces replacement, and `pkg/identity` builds the identity from it |
| Listing (C2), Cloud Storage | the bucket list, collision claim made |
| Listing (C2), other services | the service's own list (`gcplist`): 156 types, the same set choudoufu lists; binds, consistency unmeasured |
| Fallback | Cloud Asset Inventory, which lags its services; binds only, and the sweep skips it |

The corpus runs pulumi-gcp 10.2.0, and floci-gcp's list paths for Cloud SQL,
GKE and Assured Workloads are unmeasured ([#32](https://github.com/INTENTIUS/plunge/issues/32)).
