---
title: Gate
description: On Google Cloud the project is the fence. Give each stack its own.
prompt: |
  Follow https://intentius.io/plunge/gcp/gate/. Draft the project IAM bindings that hand this stack's project to its runner. Show them to me; apply nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
For most services, Google IAM conditions read Resource Manager tags and not
labels, so a label alone does not fence a write. Give each stack its own
project and bind the runner's service account to it alone.

The labels still find a stack's resources, and plunge binds and refuses by
them. Each value is the digest of `<project>/<stack>`:

```sh
printf %s 'myproject/prod' | shasum -a 256 | cut -c1-16
```

## Recorded runs

None. [#45](https://github.com/INTENTIUS/plunge/issues/45) holds a gate run for plunge's markers.
