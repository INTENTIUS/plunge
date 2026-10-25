// plunge corpus program aws-go (INTENTIUS/plunge#4). The same resources
// under the same logical names as aws-ts; the README beside this directory
// maps each one to the concurrency claim or adoption case it exercises.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/sns"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/sqs"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Site is the component resource: a client-named bucket and an autonamed
// topic parented to it, with registered outputs.
type Site struct {
	pulumi.ResourceState

	BucketName pulumi.StringOutput `pulumi:"bucketName"`
	TopicArn   pulumi.StringOutput `pulumi:"topicArn"`
}

func newSite(ctx *pulumi.Context, name, prefix string, tags pulumi.StringMap, opts ...pulumi.ResourceOption) (*Site, error) {
	site := &Site{}
	if err := ctx.RegisterComponentResource("corpus:index:Site", name, site, opts...); err != nil {
		return nil, err
	}
	assets, err := s3.NewBucket(ctx, name+"-assets", &s3.BucketArgs{
		Bucket: pulumi.String(fmt.Sprintf("%s-%s-assets", prefix, name)),
		Tags:   tags,
	}, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}
	notify, err := sns.NewTopic(ctx, name+"-notify", &sns.TopicArgs{
		Tags: tags,
	}, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}
	site.BucketName = assets.Bucket
	site.TopicArn = notify.Arn
	if err := ctx.RegisterResourceOutputs(site, pulumi.Map{
		"bucketName": assets.Bucket,
		"topicArn":   notify.Arn,
	}); err != nil {
		return nil, err
	}
	return site, nil
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "corpus")
		endpoint := cfg.Get("flociEndpoint")
		if endpoint == "" {
			endpoint = "http://localhost:4566"
		}
		prefix := cfg.Get("prefix")
		if prefix == "" {
			prefix = "plc-go"
		}
		workerCount, err := cfg.TryInt("workerCount")
		if err != nil {
			workerCount = 3
		}
		// A secret from config when the maintainer set one, else a literal
		// wrapped as a secret.
		dbPassword, err := cfg.TrySecret("dbPassword")
		if err != nil {
			dbPassword = pulumi.ToSecret(pulumi.String("corpus-placeholder-password").ToStringOutput()).(pulumi.StringOutput)
		}

		tags := pulumi.StringMap{
			"corpus-program": pulumi.String("aws-go"),
			"corpus-owner":   pulumi.String("plunge"),
		}
		withTags := func(extra map[string]string) pulumi.StringMap {
			m := pulumi.StringMap{}
			for k, v := range tags {
				m[k] = v
			}
			for k, v := range extra {
				m[k] = pulumi.String(v)
			}
			return m
		}

		// Client-named (C1).
		siteBucket, err := s3.NewBucket(ctx, "site-bucket", &s3.BucketArgs{
			Bucket: pulumi.String(prefix + "-site"),
			Tags:   tags,
		})
		if err != nil {
			return err
		}

		assume, err := json.Marshal(map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{{
				"Effect":    "Allow",
				"Action":    "sts:AssumeRole",
				"Principal": map[string]any{"Service": "ec2.amazonaws.com"},
			}},
		})
		if err != nil {
			return err
		}
		appRole, err := iam.NewRole(ctx, "app-role", &iam.RoleArgs{
			Name:             pulumi.String(prefix + "-app"),
			AssumeRolePolicy: pulumi.String(string(assume)),
			Tags:             tags,
		})
		if err != nil {
			return err
		}

		// Untaggable (coverage-only).
		sitePolicy, err := s3.NewBucketPolicy(ctx, "site-bucket-policy", &s3.BucketPolicyArgs{
			Bucket: siteBucket.ID(),
			Policy: pulumi.All(appRole.Arn, siteBucket.Arn).ApplyT(func(args []any) (string, error) {
				doc, err := json.Marshal(map[string]any{
					"Version": "2012-10-17",
					"Statement": []map[string]any{{
						"Effect":    "Allow",
						"Principal": map[string]any{"AWS": args[0].(string)},
						"Action":    []string{"s3:GetObject"},
						"Resource":  []string{args[1].(string) + "/*"},
					}},
				})
				return string(doc), err
			}).(pulumi.StringOutput),
		})
		if err != nil {
			return err
		}

		// Server-assigned IDs (C2).
		vpc, err := ec2.NewVpc(ctx, "main", &ec2.VpcArgs{
			CidrBlock: pulumi.String("10.42.0.0/16"),
			Tags:      withTags(map[string]string{"Name": prefix + "-main"}),
		})
		if err != nil {
			return err
		}
		appSg, err := ec2.NewSecurityGroup(ctx, "app-sg", &ec2.SecurityGroupArgs{
			VpcId:       vpc.ID(),
			Description: pulumi.String("plunge corpus app security group"),
			Tags:        tags,
		})
		if err != nil {
			return err
		}

		// Autonamed.
		eventsQueue, err := sqs.NewQueue(ctx, "events", &sqs.QueueArgs{
			MessageRetentionSeconds: pulumi.Int(3600),
			Tags:                    tags,
		})
		if err != nil {
			return err
		}

		// Count-like loop.
		workerURLs := pulumi.StringArray{}
		for i := 0; i < workerCount; i++ {
			q, err := sqs.NewQueue(ctx, fmt.Sprintf("worker-%d", i), &sqs.QueueArgs{
				Tags: withTags(map[string]string{"corpus-index": fmt.Sprint(i)}),
			})
			if err != nil {
				return err
			}
			workerURLs = append(workerURLs, q.Url)
		}

		// Component resource with children.
		docs, err := newSite(ctx, "docs", prefix, tags)
		if err != nil {
			return err
		}

		// Explicit provider beside the default one.
		ep := pulumi.String(endpoint)
		west, err := aws.NewProvider(ctx, "west", &aws.ProviderArgs{
			Region:                    pulumi.String("us-west-2"),
			AccessKey:                 pulumi.String("test"),
			SecretKey:                 pulumi.String("test"),
			SkipCredentialsValidation: pulumi.Bool(true),
			SkipRequestingAccountId:   pulumi.Bool(true),
			SkipRegionValidation:      pulumi.Bool(true),
			SkipMetadataApiCheck:      pulumi.Bool(true),
			S3UsePathStyle:            pulumi.Bool(true),
			Endpoints: aws.ProviderEndpointArray{
				aws.ProviderEndpointArgs{
					Ec2: ep, Iam: ep, S3: ep, Sns: ep, Sqs: ep, Ssm: ep, Sts: ep,
				},
			},
		})
		if err != nil {
			return err
		}
		westAlerts, err := sns.NewTopic(ctx, "alerts-west", &sns.TopicArgs{
			Name: pulumi.String(prefix + "-alerts-west"),
			Tags: tags,
		}, pulumi.Provider(west))
		if err != nil {
			return err
		}

		// A secret.
		dbParam, err := ssm.NewParameter(ctx, "db-password", &ssm.ParameterArgs{
			Name:  pulumi.String("/" + prefix + "/db-password"),
			Type:  pulumi.String("SecureString"),
			Value: dbPassword,
			Tags:  tags,
		})
		if err != nil {
			return err
		}

		// A resource created inside apply(): absent from a first preview,
		// since the VPC ID is unknown and the callback never runs.
		vpcParamName := vpc.ID().ApplyT(func(id pulumi.ID) (pulumi.StringOutput, error) {
			p, err := ssm.NewParameter(ctx, "vpc-id", &ssm.ParameterArgs{
				Name:  pulumi.String(fmt.Sprintf("/%s/vpc/%s", prefix, id)),
				Type:  pulumi.String("String"),
				Value: pulumi.String(string(id)),
				Tags:  tags,
			})
			if err != nil {
				return pulumi.StringOutput{}, err
			}
			return p.Name, nil
		}).(pulumi.StringOutput)

		ctx.Export("siteBucketName", siteBucket.Bucket)
		ctx.Export("sitePolicyBucket", sitePolicy.Bucket)
		ctx.Export("appRoleArn", appRole.Arn)
		ctx.Export("vpcId", vpc.ID())
		ctx.Export("securityGroupId", appSg.ID())
		ctx.Export("eventsQueueUrl", eventsQueue.Url)
		ctx.Export("workerQueueUrls", workerURLs)
		ctx.Export("docsBucketName", docs.BucketName)
		ctx.Export("docsTopicArn", docs.TopicArn)
		ctx.Export("westAlertsArn", westAlerts.Arn)
		ctx.Export("dbPasswordParameter", dbParam.Name)
		ctx.Export("dbPasswordValue", pulumi.ToSecret(dbPassword))
		ctx.Export("vpcParameterName", vpcParamName)
		return nil
	})
}
