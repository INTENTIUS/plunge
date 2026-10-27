---
title: Gate
description: On Google Cloud the project is the fence. Give each stack its own.
prompt: |
  Follow https://intentius.io/plunge/gcp/gate/. Draft the project IAM bindings that hand this stack's project to its runner. Show them to me; apply nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
Google IAM conditions read resource tags (Resource Manager tags), not labels,
for most services, so a label alone does not fence a write. As on
choudoufu's GCP pages, give each stack its own project and bind the runner's
service account to that project only.

The labels still find a stack's resources, and plunge binds and refuses by
them. A label's value is the digest of `<project>/<stack>`:

```sh
printf %s 'myproject/prod' | shasum -a 256 | cut -c1-16
```

## What has run

Nothing. [#45](https://github.com/INTENTIUS/plunge/issues/45) holds a gate run for plunge's markers.
