# Third-party code in plunge

plunge is a fork of pulumi/pulumi and is Apache-2.0 (`LICENSE`). This file
lists the code plunge adds that is under another license. Upstream's own
dependencies are listed by their modules' `go.mod` files and are not repeated
here.

## choudoufu (MPL-2.0), a module dependency

`pkg/go.mod` requires `github.com/intentius/choudoufu`, pinned to a
pseudo-version of a choudoufu commit. plunge imports these packages from it:

| Package | Used for |
|---|---|
| `pkg/staterecord` | the record stores behind `plunge+file://`, `plunge+s3://`, `plunge+azblob://`, `plunge+gs://` and `plunge+k8s://`, and the estate-boundary admission policy it embeds |
| `pkg/cloudcontrol` | the AWS Cloud Control lister |
| `pkg/kubesweep` | the Kubernetes lister and the `plunge+k8s://` cluster connection |
| `pkg/registry` | the pulumi-aws to CloudFormation type roster |
| `pkg/gcpsweep` | the Cloud Asset Inventory lister and GCP provider attributes |
| `pkg/azsweep` | Azure provider attributes |
| `pkg/identity` | import ids and Asset names per type |
| `pkg/markerkeys` | the marker-key seam plunge passes its keys through |

These files are MPL-2.0, file by file, as each file's header says
(`SPDX-License-Identifier: MPL-2.0`), with HashiCorp and OpenTofu
copyrights. The MPL text is at https://mozilla.org/MPL/2.0/. MPL-2.0 applies
per file, so the Apache-2.0 code in this repository that imports them does
not take it on.

Until INTENTIUS/plunge#39, copies of the first six packages sat under
`pkg/backend/plunge/`, each with a NOTICE. They are gone. The MPL-2.0 files now
arrive through the module dependency, in source form, at the commit `pkg/go.mod`
names, so anyone holding a plunge binary can get the exact source of the
MPL-2.0 files it was built from.
