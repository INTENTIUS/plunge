---
title: Gate
description: A role on the stack resource groups grants access, and Azure Policy can check the tag.
prompt: |
  Follow https://intentius.io/plunge/azure/gate/. Draft the role assignment and the Azure Policy rule for this stack. Show them to me; apply nothing.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
Put each stack in its own resource groups and scope the runner's role to
them. Azure Policy can deny a write whose `plunge-stack` tag names another
stack:

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
