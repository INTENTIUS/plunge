// plunge corpus program aws-ts (INTENTIUS/plunge#4).
//
// Every resource below is here for a concurrency claim (C1-C6 in the parity
// table on INTENTIUS/plunge#2) or for adoption (P-L4). The README beside this
// directory maps each one to what it exercises. The other aws-* programs
// declare the same resources under the same logical names, so their previews
// can be compared step by step.
import * as pulumi from "@pulumi/pulumi";
import * as aws from "@pulumi/aws";

const cfg = new pulumi.Config("corpus");
const endpoint = cfg.get("flociEndpoint") ?? "http://localhost:4566";
const prefix = cfg.get("prefix") ?? "plc-ts";
const workerCount = cfg.getNumber("workerCount") ?? 3;
const eventsRetentionSeconds = cfg.getNumber("eventsRetentionSeconds") ?? 3600;
// A secret from config when the maintainer set one, else a literal wrapped
// as a secret, so the baseline never depends on a ciphertext in the stack file.
const dbPassword = cfg.getSecret("dbPassword") ?? pulumi.secret("corpus-placeholder-password");

const tags = { "corpus-program": "aws-ts", "corpus-owner": "plunge" };

// Client-named (C1): the name is ours, so two creates collide on S3's and
// IAM's uniqueness constraints.
const siteBucket = new aws.s3.Bucket("site-bucket", {
    bucket: `${prefix}-site`,
    tags,
});

const appRole = new aws.iam.Role("app-role", {
    name: `${prefix}-app`,
    assumeRolePolicy: JSON.stringify({
        Version: "2012-10-17",
        Statement: [{ Effect: "Allow", Action: "sts:AssumeRole", Principal: { Service: "ec2.amazonaws.com" } }],
    }),
    tags,
});

// Untaggable (coverage-only): a bucket policy has no tags argument, so a
// marker can't land on it and ownership has to ride in the record.
const sitePolicy = new aws.s3.BucketPolicy("site-bucket-policy", {
    bucket: siteBucket.id,
    policy: pulumi.jsonStringify({
        Version: "2012-10-17",
        Statement: [{
            Effect: "Allow",
            Principal: { AWS: appRole.arn },
            Action: ["s3:GetObject"],
            Resource: [pulumi.interpolate`${siteBucket.arn}/*`],
        }],
    }),
});

// Server-assigned ID (C2): AWS picks vpc-... and sg-..., nothing the program
// says can collide, so two creates make two VPCs.
const vpc = new aws.ec2.Vpc("main", {
    cidrBlock: "10.42.0.0/16",
    tags: { ...tags, Name: `${prefix}-main` },
});

const appSg = new aws.ec2.SecurityGroup("app-sg", {
    vpcId: vpc.id,
    description: "plunge corpus app security group",
    tags,
});

// Autonamed (C1 vs C2, the open autonaming decision): no name argument, so
// the engine adds a random suffix in Check.
const eventsQueue = new aws.sqs.Queue("events", {
    messageRetentionSeconds: eventsRetentionSeconds,
    tags,
});

// Count-like loop: N autonamed queues whose logical names carry the index.
const workers: aws.sqs.Queue[] = [];
for (let i = 0; i < workerCount; i++) {
    workers.push(new aws.sqs.Queue(`worker-${i}`, {
        tags: { ...tags, "corpus-index": `${i}` },
    }));
}

// A component resource with children. Its children's URNs carry the
// component type in their parent chain, and it registers outputs.
interface SiteArgs {
    prefix: string;
    tags: Record<string, string>;
}

class Site extends pulumi.ComponentResource {
    public readonly bucketName: pulumi.Output<string>;
    public readonly topicArn: pulumi.Output<string>;

    constructor(name: string, args: SiteArgs, opts?: pulumi.ComponentResourceOptions) {
        super("corpus:index:Site", name, {}, opts);
        const assets = new aws.s3.Bucket(`${name}-assets`, {
            bucket: `${args.prefix}-${name}-assets`,
            tags: args.tags,
        }, { parent: this });
        const notify = new aws.sns.Topic(`${name}-notify`, {
            tags: args.tags,
        }, { parent: this });
        this.bucketName = assets.bucket;
        this.topicArn = notify.arn;
        this.registerOutputs({ bucketName: this.bucketName, topicArn: this.topicArn });
    }
}

const docs = new Site("docs", { prefix, tags });

// An explicit provider beside the default one the stack config sets up. It
// carries its own floci wiring, since explicit providers don't read the
// aws: config namespace.
const west = new aws.Provider("west", {
    region: "us-west-2",
    accessKey: "test",
    secretKey: "test",
    skipCredentialsValidation: true,
    skipRequestingAccountId: true,
    skipRegionValidation: true,
    skipMetadataApiCheck: true,
    s3UsePathStyle: true,
    endpoints: [{
        ec2: endpoint,
        iam: endpoint,
        s3: endpoint,
        sns: endpoint,
        sqs: endpoint,
        ssm: endpoint,
        sts: endpoint,
    }],
});

const westAlerts = new aws.sns.Topic("alerts-west", {
    name: `${prefix}-alerts-west`,
    tags,
}, { provider: west });

// A secret: the SecureString's value and the stack output below are both
// secret in the checkpoint.
const dbParam = new aws.ssm.Parameter("db-password", {
    name: `/${prefix}/db-password`,
    type: aws.ssm.ParameterType.SecureString,
    value: dbPassword,
    tags,
});

// A resource created inside apply(). Its name depends on the VPC's
// server-assigned ID, so on a first preview the callback never runs and this
// parameter is absent from the preview; it only appears at `up`.
const vpcParam = vpc.id.apply(id => new aws.ssm.Parameter("vpc-id", {
    name: `/${prefix}/vpc/${id}`,
    type: aws.ssm.ParameterType.String,
    value: id,
    tags,
}));

// Stack outputs. vpcId and securityGroupId are unknown at a first preview.
export const siteBucketName = siteBucket.bucket;
export const sitePolicyBucket = sitePolicy.bucket;
export const appRoleArn = appRole.arn;
export const vpcId = vpc.id;
export const securityGroupId = appSg.id;
export const eventsQueueUrl = eventsQueue.url;
export const workerQueueUrls = workers.map(q => q.url);
export const docsBucketName = docs.bucketName;
export const docsTopicArn = docs.topicArn;
export const westAlertsArn = westAlerts.arn;
export const dbPasswordParameter = dbParam.name;
export const dbPasswordValue = pulumi.secret(dbPassword);
export const vpcParameterName = vpcParam.apply(p => p.name);
