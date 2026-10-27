---
title: Gate
description: An IAM condition on the plunge-stack tag can scope a role to one stack. Not yet run against plunge.
prompt: |
  Follow https://intentius.io/plunge/aws/gate/. Draft an IAM policy scoping one role to this stack and show it to me.
  Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.
---
The create call writes `plunge-stack`, so an IAM policy can condition on it.
The value is `<project>/<stack>`, readable as written.

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

Which actions honour `aws:ResourceTag` and `aws:RequestTag` differs by
service; the service authorization reference lists them.

## What has run

Nothing. No policy has been run against plunge's markers on floci or AWS.
[#45](https://github.com/INTENTIUS/plunge/issues/45) holds that run. Until then this page shows the shape of the policy
and makes no claim that it fences plunge's writes.

## Guarding the marker

An SCP that denies removing tags whose key starts with `plunge-` keeps the
markers on resources in member accounts. plunge itself refuses a provider
configuration that would hide them ([Adopt](/plunge/aws/adopt/)).
