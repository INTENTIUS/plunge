"""plunge corpus program aws-python (INTENTIUS/plunge#4).

The same resources under the same logical names as aws-ts; the README beside
this directory maps each one to the concurrency claim or adoption case it
exercises.
"""

import json

import pulumi
import pulumi_aws as aws

cfg = pulumi.Config("corpus")
endpoint = cfg.get("flociEndpoint") or "http://localhost:4566"
prefix = cfg.get("prefix") or "plc-python"
worker_count = cfg.get_int("workerCount") or 3
events_retention_seconds = cfg.get_int("eventsRetentionSeconds") or 3600
# A secret from config when the maintainer set one, else a literal wrapped as
# a secret.
db_password = cfg.get_secret("dbPassword") or pulumi.Output.secret("corpus-placeholder-password")

tags = {"corpus-program": "aws-python", "corpus-owner": "plunge"}

# Client-named (C1).
site_bucket = aws.s3.Bucket("site-bucket", bucket=f"{prefix}-site", tags=tags)

app_role = aws.iam.Role(
    "app-role",
    name=f"{prefix}-app",
    assume_role_policy=json.dumps(
        {
            "Version": "2012-10-17",
            "Statement": [
                {
                    "Effect": "Allow",
                    "Action": "sts:AssumeRole",
                    "Principal": {"Service": "ec2.amazonaws.com"},
                }
            ],
        }
    ),
    tags=tags,
)

# Untaggable (coverage-only).
site_policy = aws.s3.BucketPolicy(
    "site-bucket-policy",
    bucket=site_bucket.id,
    policy=pulumi.Output.json_dumps(
        {
            "Version": "2012-10-17",
            "Statement": [
                {
                    "Effect": "Allow",
                    "Principal": {"AWS": app_role.arn},
                    "Action": ["s3:GetObject"],
                    "Resource": [pulumi.Output.concat(site_bucket.arn, "/*")],
                }
            ],
        }
    ),
)

# Server-assigned IDs (C2).
vpc = aws.ec2.Vpc("main", cidr_block="10.42.0.0/16", tags={**tags, "Name": f"{prefix}-main"})

app_sg = aws.ec2.SecurityGroup(
    "app-sg",
    vpc_id=vpc.id,
    description="plunge corpus app security group",
    tags=tags,
)

# Autonamed.
events_queue = aws.sqs.Queue("events", message_retention_seconds=events_retention_seconds, tags=tags)

# Count-like loop.
workers = [
    aws.sqs.Queue(f"worker-{i}", tags={**tags, "corpus-index": str(i)})
    for i in range(worker_count)
]


class Site(pulumi.ComponentResource):
    """The component resource: a client-named bucket and an autonamed topic
    parented to it, with registered outputs."""

    def __init__(self, name: str, prefix: str, tags: dict, opts: pulumi.ResourceOptions | None = None):
        super().__init__("corpus:index:Site", name, None, opts)
        child = pulumi.ResourceOptions(parent=self)
        assets = aws.s3.Bucket(f"{name}-assets", bucket=f"{prefix}-{name}-assets", tags=tags, opts=child)
        notify = aws.sns.Topic(f"{name}-notify", tags=tags, opts=child)
        self.bucket_name = assets.bucket
        self.topic_arn = notify.arn
        self.register_outputs({"bucketName": self.bucket_name, "topicArn": self.topic_arn})


docs = Site("docs", prefix, tags)

# Explicit provider beside the default one.
west = aws.Provider(
    "west",
    region="us-west-2",
    access_key="test",
    secret_key="test",
    skip_credentials_validation=True,
    skip_requesting_account_id=True,
    skip_region_validation=True,
    skip_metadata_api_check=True,
    s3_use_path_style=True,
    endpoints=[
        aws.ProviderEndpointArgs(
            ec2=endpoint,
            iam=endpoint,
            s3=endpoint,
            sns=endpoint,
            sqs=endpoint,
            ssm=endpoint,
            sts=endpoint,
        )
    ],
)

west_alerts = aws.sns.Topic(
    "alerts-west",
    name=f"{prefix}-alerts-west",
    tags=tags,
    opts=pulumi.ResourceOptions(provider=west),
)

# A secret.
db_param = aws.ssm.Parameter(
    "db-password",
    name=f"/{prefix}/db-password",
    type="SecureString",
    value=db_password,
    tags=tags,
)


# A resource created inside apply(): absent from a first preview, since the
# VPC ID is unknown and the callback never runs.
def _vpc_param(vpc_id: str) -> aws.ssm.Parameter:
    return aws.ssm.Parameter(
        "vpc-id",
        name=f"/{prefix}/vpc/{vpc_id}",
        type="String",
        value=vpc_id,
        tags=tags,
    )


vpc_param = vpc.id.apply(_vpc_param)

pulumi.export("siteBucketName", site_bucket.bucket)
pulumi.export("sitePolicyBucket", site_policy.bucket)
pulumi.export("appRoleArn", app_role.arn)
pulumi.export("vpcId", vpc.id)
pulumi.export("securityGroupId", app_sg.id)
pulumi.export("eventsQueueUrl", events_queue.url)
pulumi.export("workerQueueUrls", [q.url for q in workers])
pulumi.export("docsBucketName", docs.bucket_name)
pulumi.export("docsTopicArn", docs.topic_arn)
pulumi.export("westAlertsArn", west_alerts.arn)
pulumi.export("dbPasswordParameter", db_param.name)
pulumi.export("dbPasswordValue", pulumi.Output.secret(db_password))
pulumi.export("vpcParameterName", vpc_param.apply(lambda p: p.name))
