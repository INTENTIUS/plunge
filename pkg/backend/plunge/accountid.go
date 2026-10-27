// Copyright 2026, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package plunge

// The AWS account ID a Cloud Control translation needs (INTENTIUS/plunge#31). aws_budgets_budget_action's Terraform
// id starts with the account ID ("<account id>:<action id>:<budget name>"), which neither its Cloud Control
// identifier nor its model carries. plunge reads it with STS GetCallerIdentity, as the provider's own credentials see
// it, on the stack's own STS endpoint, under #27's endpoint rules (endpoints.go): the provider's endpoints entry for
// sts, then the AWS_ENDPOINT_URL_STS and AWS_ENDPOINT_URL variables the provider reads. When the stack names custom
// endpoints but none for STS, the listing is skipped with a warning, and plunge never calls STS's public endpoint.
//
// A provider whose allowedAccountIds names exactly one account needs no call: the provider refuses to run as any
// other.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// stsEndpoint is where plunge calls STS for a provider with these inputs: "" for STS's public regional endpoint,
// which is used only when nothing in the configuration names a custom endpoint. A non-empty skip says why there is
// no endpoint to call although the configuration names custom ones.
func stsEndpoint(inputs resource.PropertyMap) (endpoint, skip string) {
	eps := awsEndpoints(inputs)
	if v := eps["sts"]; v != "" {
		return v, ""
	}
	for _, n := range []string{"AWS_ENDPOINT_URL_STS", "AWS_ENDPOINT_URL"} {
		if v := os.Getenv(n); v != "" {
			return v, ""
		}
	}
	if len(eps) > 0 {
		return "", fmt.Sprintf("the aws provider's endpoints name %s and no sts entry (set one in aws:endpoints)",
			strings.Join(sortedKeys(eps), ", "))
	}
	if names := envNamesWith("AWS_ENDPOINT_URL_", ""); len(names) > 0 {
		return "", fmt.Sprintf("the environment sets %s and not AWS_ENDPOINT_URL_STS", strings.Join(names, ", "))
	}
	if v := os.Getenv(EnvCloudControlEndpoint); v != "" {
		return "", fmt.Sprintf("%s points Cloud Control at %s and nothing names an STS endpoint (set an sts entry "+
			"in aws:endpoints, or AWS_ENDPOINT_URL_STS)", EnvCloudControlEndpoint, v)
	}
	return "", ""
}

// accountIDs resolves and caches the account ID per STS endpoint, region and credentials.
type accountIDs struct {
	cache listerCache[string]
	// call is GetCallerIdentity; nil is callerAccount. Tests replace it.
	call func(ctx context.Context, region, endpoint string, creds aws.CredentialsProvider) (string, error)
}

// get is the account ID the provider with req's inputs runs as. A *SkippedListing error means the stack names
// custom endpoints and none for STS.
func (a *accountIDs) get(ctx context.Context, urn resource.URN, inputs resource.PropertyMap, region string) (
	string, error,
) {
	if ids := providerStrings(inputs, "allowedAccountIds"); len(ids) == 1 {
		return ids[0], nil
	}
	endpoint, skip := stsEndpoint(inputs)
	if skip != "" {
		return "", skipped(urn, "AWS STS", "%s", skip)
	}
	if r := providerString(inputs, "stsRegion"); r != "" {
		region = r
	}
	creds, credKey, err := stsCredentials(ctx, inputs, region, endpoint)
	if err != nil {
		return "", err
	}
	acct, err := a.cache.get(region+"|"+endpoint+"|"+credKey, func() (*string, error) {
		call := a.call
		if call == nil {
			call = callerAccount
		}
		id, err := call(ctx, region, endpoint, creds)
		if err != nil {
			return nil, fmt.Errorf("reading the account ID with STS GetCallerIdentity: %w", err)
		}
		if id == "" {
			return nil, errors.New("STS GetCallerIdentity returned no account ID")
		}
		return &id, nil
	})
	if err != nil {
		return "", err
	}
	return *acct, nil
}

// stsCredentials are the credentials the call is signed with: the provider's accessKey, secretKey and token, then
// the AWS_ACCESS_KEY_ID variables. Against the public endpoint, with neither, the default chain under the
// provider's profile. Against a custom endpoint, with neither, the call is unsigned: the default chain is not
// resolved for an emulator. key tells the cache apart by access key.
func stsCredentials(ctx context.Context, inputs resource.PropertyMap, region, endpoint string) (
	creds aws.CredentialsProvider, key string, err error,
) {
	ak, sk, tok := providerString(inputs, "accessKey"), providerString(inputs, "secretKey"),
		providerString(inputs, "token")
	if ak == "" || sk == "" {
		ak, sk, tok = os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_SESSION_TOKEN")
	}
	if ak != "" && sk != "" {
		return credentials.NewStaticCredentialsProvider(ak, sk, tok), "static:" + ak, nil
	}
	if endpoint != "" {
		return aws.AnonymousCredentials{}, "anonymous", nil
	}
	opts := []func(*config.LoadOptions) error{config.WithRegion(region)}
	profile := providerString(inputs, "profile")
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, "", fmt.Errorf("loading AWS credentials for STS: %w", err)
	}
	return cfg.Credentials, "chain:" + profile, nil
}

// callerAccount calls STS GetCallerIdentity at endpoint ("" for the public regional endpoint) and returns the
// account.
func callerAccount(ctx context.Context, region, endpoint string, creds aws.CredentialsProvider) (string, error) {
	if region == "" {
		region = "us-east-1"
	}
	transport := listerTransport
	if transport == nil {
		transport = http.DefaultTransport
	}
	opts := sts.Options{
		Region:      region,
		Credentials: creds,
		HTTPClient:  &http.Client{Transport: transport},
	}
	if endpoint != "" {
		opts.BaseEndpoint = aws.String(endpoint)
	}
	out, err := sts.New(opts).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.Account), nil
}

// providerStrings reads a list-of-strings provider input. A default provider carries a list config value as its
// JSON text.
func providerStrings(inputs resource.PropertyMap, key string) []string {
	v, ok := inputs[resource.PropertyKey(key)]
	if !ok {
		return nil
	}
	v = unwrapValue(v)
	var out []string
	switch {
	case v.IsArray():
		for _, e := range v.ArrayValue() {
			if e = unwrapValue(e); e.IsString() && e.StringValue() != "" {
				out = append(out, e.StringValue())
			}
		}
	case v.IsString():
		s := strings.TrimSpace(v.StringValue())
		if strings.HasPrefix(s, "[") {
			var list []string
			if json.Unmarshal([]byte(s), &list) == nil {
				for _, e := range list {
					if e != "" {
						out = append(out, e)
					}
				}
			}
		} else if s != "" {
			out = append(out, s)
		}
	}
	return out
}
