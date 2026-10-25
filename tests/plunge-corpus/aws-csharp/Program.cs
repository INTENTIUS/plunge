// plunge corpus program aws-csharp (INTENTIUS/plunge#4). The same resources
// under the same logical names as aws-ts; the README beside this directory
// maps each one to the concurrency claim or adoption case it exercises.
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using Pulumi;
using Aws = Pulumi.Aws;

return await Deployment.RunAsync(() =>
{
    var cfg = new Config("corpus");
    var endpoint = cfg.Get("flociEndpoint") ?? "http://localhost:4566";
    var prefix = cfg.Get("prefix") ?? "plc-csharp";
    var workerCount = cfg.GetInt32("workerCount") ?? 3;
    var eventsRetentionSeconds = cfg.GetInt32("eventsRetentionSeconds") ?? 3600;
    // A secret from config when the maintainer set one, else a literal
    // wrapped as a secret.
    var dbPassword = cfg.GetSecret("dbPassword") ?? Output.CreateSecret("corpus-placeholder-password");

    var tags = new Dictionary<string, string>
    {
        ["corpus-program"] = "aws-csharp",
        ["corpus-owner"] = "plunge",
    };
    InputMap<string> WithTags(params (string Key, string Value)[] extra)
    {
        var m = new InputMap<string>();
        foreach (var kv in tags) m.Add(kv.Key, kv.Value);
        foreach (var (k, v) in extra) m.Add(k, v);
        return m;
    }

    // Client-named (C1).
    var siteBucket = new Aws.S3.Bucket("site-bucket", new()
    {
        BucketName = $"{prefix}-site",
        Tags = WithTags(),
    });

    var appRole = new Aws.Iam.Role("app-role", new()
    {
        Name = $"{prefix}-app",
        AssumeRolePolicy = JsonSerializer.Serialize(new
        {
            Version = "2012-10-17",
            Statement = new[]
            {
                new
                {
                    Effect = "Allow",
                    Action = "sts:AssumeRole",
                    Principal = new { Service = "ec2.amazonaws.com" },
                },
            },
        }),
        Tags = WithTags(),
    });

    // Untaggable (coverage-only).
    var sitePolicy = new Aws.S3.BucketPolicy("site-bucket-policy", new()
    {
        Bucket = siteBucket.Id,
        Policy = Output.Tuple(appRole.Arn, siteBucket.Arn).Apply(t => JsonSerializer.Serialize(new
        {
            Version = "2012-10-17",
            Statement = new[]
            {
                new
                {
                    Effect = "Allow",
                    Principal = new { AWS = t.Item1 },
                    Action = new[] { "s3:GetObject" },
                    Resource = new[] { $"{t.Item2}/*" },
                },
            },
        })),
    });

    // Server-assigned IDs (C2).
    var vpc = new Aws.Ec2.Vpc("main", new()
    {
        CidrBlock = "10.42.0.0/16",
        Tags = WithTags(("Name", $"{prefix}-main")),
    });

    var appSg = new Aws.Ec2.SecurityGroup("app-sg", new()
    {
        VpcId = vpc.Id,
        Description = "plunge corpus app security group",
        Tags = WithTags(),
    });

    // Autonamed.
    var eventsQueue = new Aws.Sqs.Queue("events", new()
    {
        MessageRetentionSeconds = eventsRetentionSeconds,
        Tags = WithTags(),
    });

    // Count-like loop.
    var workers = Enumerable.Range(0, workerCount)
        .Select(i => new Aws.Sqs.Queue($"worker-{i}", new()
        {
            Tags = WithTags(("corpus-index", i.ToString())),
        }))
        .ToList();

    // Component resource with children.
    var docs = new Site("docs", prefix, tags);

    // Explicit provider beside the default one.
    var west = new Aws.Provider("west", new()
    {
        Region = "us-west-2",
        AccessKey = "test",
        SecretKey = "test",
        SkipCredentialsValidation = true,
        SkipRequestingAccountId = true,
        SkipRegionValidation = true,
        SkipMetadataApiCheck = true,
        S3UsePathStyle = true,
        Endpoints = new[]
        {
            new Aws.Inputs.ProviderEndpointArgs
            {
                Ec2 = endpoint,
                Iam = endpoint,
                S3 = endpoint,
                Sns = endpoint,
                Sqs = endpoint,
                Ssm = endpoint,
                Sts = endpoint,
            },
        },
    });

    var westAlerts = new Aws.Sns.Topic("alerts-west", new()
    {
        Name = $"{prefix}-alerts-west",
        Tags = WithTags(),
    }, new CustomResourceOptions { Provider = west });

    // A secret.
    var dbParam = new Aws.Ssm.Parameter("db-password", new()
    {
        Name = $"/{prefix}/db-password",
        Type = "SecureString",
        Value = dbPassword,
        Tags = WithTags(),
    });

    // A resource created inside apply(): absent from a first preview, since
    // the VPC ID is unknown and the callback never runs.
    var vpcParam = vpc.Id.Apply(id => new Aws.Ssm.Parameter("vpc-id", new()
    {
        Name = $"/{prefix}/vpc/{id}",
        Type = "String",
        Value = id,
        Tags = WithTags(),
    }));

    return new Dictionary<string, object?>
    {
        ["siteBucketName"] = siteBucket.BucketName,
        ["sitePolicyBucket"] = sitePolicy.Bucket,
        ["appRoleArn"] = appRole.Arn,
        ["vpcId"] = vpc.Id,
        ["securityGroupId"] = appSg.Id,
        ["eventsQueueUrl"] = eventsQueue.Url,
        ["workerQueueUrls"] = Output.All(workers.Select(q => q.Url)),
        ["docsBucketName"] = docs.BucketName,
        ["docsTopicArn"] = docs.TopicArn,
        ["westAlertsArn"] = westAlerts.Arn,
        ["dbPasswordParameter"] = dbParam.Name,
        ["dbPasswordValue"] = Output.CreateSecret(dbPassword),
        ["vpcParameterName"] = vpcParam.Apply(p => p.Name),
    };
});

// The component resource: a client-named bucket and an autonamed topic
// parented to it, with registered outputs.
class Site : ComponentResource
{
    [Output("bucketName")]
    public Output<string> BucketName { get; private set; }

    [Output("topicArn")]
    public Output<string> TopicArn { get; private set; }

    public Site(string name, string prefix, Dictionary<string, string> tags, ComponentResourceOptions? opts = null)
        : base("corpus:index:Site", name, opts)
    {
        var child = new CustomResourceOptions { Parent = this };
        var assets = new Aws.S3.Bucket($"{name}-assets", new()
        {
            BucketName = $"{prefix}-{name}-assets",
            Tags = tags,
        }, child);
        var notify = new Aws.Sns.Topic($"{name}-notify", new()
        {
            Tags = tags,
        }, child);
        BucketName = assets.BucketName;
        TopicArn = notify.Arn;
        RegisterOutputs(new Dictionary<string, object?>
        {
            ["bucketName"] = BucketName,
            ["topicArn"] = TopicArn,
        });
    }
}
