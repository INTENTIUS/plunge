---
title: Gate
description: An IAM condition on the plunge-stack tag can scope a role to one stack.
prompt: |
  Follow https://intentius.io/plunge/aws/gate/. Draft an IAM policy scoping one role to this stack and show it to me.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
The create call writes `plunge-stack` as `<project>/<stack>`, so an IAM
policy can condition on it.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ChangeOnlyThisStack",
      "Effect": "Allow",
      "Action": ["s3:PutBucketTagging", "s3:DeleteBucket", "sqs:SetQueueAttributes", "sqs:DeleteQueue"],
      "Resource": "*",
      "Condition": {
        "StringEquals": {"aws:ResourceTag/plunge-stack": "myproject/prod"}
      }
    },
    {
      "Sid": "CreateOnlyIntoThisStack",
      "Effect": "Allow",
      "Action": ["sqs:CreateQueue", "sqs:TagQueue"],
      "Resource": "*",
      "Condition": {
        "StringEquals": {"aws:RequestTag/plunge-stack": "myproject/prod"}
      }
    }
  ]
}
```

This shape has run against real AWS for SQS queues. A role with it could create
and change a queue marked with its own stack and was refused by SQS on another
stack's queue and on a create under another stack's marker; the role also
needed `cloudformation:ListResources` and `cloudformation:GetResource` for
plunge's bind sweep. The S3 actions above have not been run. The recording is
in `tests/plunge-gate/aws-iam/` in the repository
([#45](https://github.com/INTENTIUS/plunge/issues/45)).

Which actions honour `aws:ResourceTag` and `aws:RequestTag` differs by
service; the service authorization reference lists them.

## Guarding the marker

An SCP denying removal of `plunge-` tag keys keeps the markers in member
accounts. plunge refuses a provider configuration that would hide them
([Adopt](/plunge/aws/adopt/)).
