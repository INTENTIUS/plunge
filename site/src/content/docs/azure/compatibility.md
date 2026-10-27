---
title: Compatibility
description: Which Azure providers and types take markers, and how ARM lists them.
prompt: |
  Follow https://intentius.io/plunge/azure/compatibility/. List the Azure resource types in this program and say for each whether it takes tags. Change no files.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
| Provider | Markers | Names (C1) | Listing (C2) |
|---|---|---|---|
| pulumi-azure | map `tags`, from the bridge's Terraform schema | not covered: `pkg/identity` has no azurerm rows | ARM, filtered by `plunge-urn`; binds, consistency unmeasured |
| azure-native | map `tags`, from its schema distilled once per version and cached (`PLUNGE_AZURE_NATIVE_CACHE`) | not covered: no autoname schema is read | ARM, as above |

The corpus runs pulumi-azure 6.10.0 ([#16](https://github.com/INTENTIUS/plunge/issues/16)), and since Azure keeps a tag key
as its writer cased it, plunge ignores case in marker keys.

## Record store

On open, `plunge+azblob://` checks blob versioning and anonymous access and
looks for a lifecycle rule on old versions. With `endpoint` or `metadata_host`
it calls only the URL's endpoints ([#35](https://github.com/INTENTIUS/plunge/issues/35)).
