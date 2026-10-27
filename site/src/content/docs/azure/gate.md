---
title: Gate
description: A role on the stack resource groups grants access, and Azure Policy can check the tag. Not yet run against plunge.
prompt: |
  Follow https://intentius.io/plunge/azure/gate/. Draft the role assignment and the Azure Policy rule for this stack. Show them to me; apply nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
As on choudoufu's Azure pages, put a stack's resources in resource groups of
its own and scope the runner's role to them. Azure Policy can then deny a
write whose `plunge-stack` tag names another stack:

```json
{
  "if": {
    "allOf": [
      { "field": "tags['plunge-stack']", "exists": "true" },
      { "field": "tags['plunge-stack']", "notEquals": "myproject/prod" }
    ]
  },
  "then": { "effect": "deny" }
}
```

## What has run

Nothing. [#45](https://github.com/INTENTIUS/plunge/issues/45) holds a gate run for plunge's markers.
