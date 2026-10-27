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

// The plunge+s3:// and plunge+gs:// stores against an emulator (INTENTIUS/plunge#50), by plunge#35's rule for
// plunge+azblob:// (azblobstore.go): a URL that names a custom endpoint gets static credentials from the
// environment and nothing else. The SDKs' default chains are never built for it, since they can reach a host that
// is not the endpoint: the EC2 instance metadata service (169.254.169.254), SSO and STS for AWS; the GCE metadata
// server (metadata.google.internal, 169.254.169.254) and oauth2.googleapis.com for Google.
//
//   - plunge+s3:// is custom when it has an endpoint query key, or when AWS_ENDPOINT_URL_S3 or AWS_ENDPOINT_URL is
//     set. Its client signs with AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY (and AWS_SESSION_TOKEN when set) and
//     nothing else. The open is refused when either key is missing, and when the URL names a profile, since a
//     profile can resolve through SSO or an assumed role.
//   - plunge+gs:// is custom when STORAGE_EMULATOR_HOST is set. Its client sends GOOGLE_OAUTH_ACCESS_TOKEN as a
//     static bearer token when set, else no credential at all. An endpoint query key is refused and names
//     STORAGE_EMULATOR_HOST instead: the DIY backend opens the same bucket for the stack metadata through go-cloud,
//     which refuses query keys it does not know and follows only STORAGE_EMULATOR_HOST, so an endpoint key would
//     leave the metadata on real Google Cloud Storage.
//
// The stack metadata half (diy.New, an upstream file) builds its own clients. New runs refuseDefaultChains on the
// metadata URL first, so the open is refused before diy.New when the environment would send go-cloud to a default
// chain: for s3 that is the two AWS keys (go-cloud's chain takes static environment keys first and resolves nothing
// else when they are set, unless a profile key is given); for gs it is GOOGLE_OAUTH_ACCESS_TOKEN or
// GOOGLE_CREDENTIALS, which the DIY backend's authhelpers.ResolveGoogleCredentials reads before it falls back to
// Application Default Credentials. go-cloud sends no credential to STORAGE_EMULATOR_HOST either way.
//
// choudoufu's pkg/staterecord (plunge#39) takes the *s3.Client and *storage.Client from the caller
// (S3Config.Client, GCSConfig.Client) and builds no credential of its own, so nothing here needs a choudoufu change.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

// localStoreTransport is what a plunge+s3:// or plunge+gs:// store with a custom endpoint sends its requests over.
// Nil is http.DefaultTransport. Tests set it to a transport that fails on any host that is not the loopback address.
var localStoreTransport http.RoundTripper

func localTransport() http.RoundTripper {
	if localStoreTransport != nil {
		return localStoreTransport
	}
	return http.DefaultTransport
}

// s3CustomEndpoint is the endpoint a plunge+s3:// URL's client goes to when it is not AWS's own: the endpoint query
// key, else AWS_ENDPOINT_URL_S3, else AWS_ENDPOINT_URL, which the SDK would otherwise read itself. "" is real S3.
func s3CustomEndpoint(q url.Values) string {
	if e := q.Get("endpoint"); e != "" {
		return e
	}
	_, e := firstNonEmptyEnv("AWS_ENDPOINT_URL_S3", "AWS_ENDPOINT_URL")
	return e
}

// s3StaticCredentials is the credential for a plunge+s3:// URL with a custom endpoint: the access key pair (and
// session token) from the environment, or the refusal that says what to set.
func s3StaticCredentials(plungeURL, endpoint string, q url.Values) (aws.CredentialsProvider, error) {
	if p := q.Get("profile"); p != "" {
		return nil, fmt.Errorf("plunge: %q names the endpoint %s and the profile %q; with a custom endpoint plunge "+
			"signs only with AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY from the environment, since a profile can "+
			"resolve through SSO or an assumed role on AWS's own hosts. Drop the profile key and set the two "+
			"variables", plungeURL, endpoint, p)
	}
	id, secret := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY")
	if id == "" || secret == "" {
		return nil, fmt.Errorf("plunge: %q names the endpoint %s and no credential for it: set AWS_ACCESS_KEY_ID and "+
			"AWS_SECRET_ACCESS_KEY (and AWS_SESSION_TOKEN if the endpoint wants one). With a custom endpoint, plunge "+
			"never falls back to the AWS default credential chain (instance metadata, SSO, STS)", plungeURL, endpoint)
	}
	return aws.NewCredentialsCache(
		credentials.NewStaticCredentialsProvider(id, secret, os.Getenv("AWS_SESSION_TOKEN"))), nil
}

// s3CustomClient is the S3 client for a plunge+s3:// URL with a custom endpoint, built from an aws.Config by hand so
// the default configuration loader (and its credential chain) never runs. The region is the region query key, else
// AWS_REGION or AWS_DEFAULT_REGION, else us-east-1, which emulators accept.
func s3CustomClient(plungeURL, endpoint string, q url.Values) (*s3.Client, error) {
	creds, err := s3StaticCredentials(plungeURL, endpoint, q)
	if err != nil {
		return nil, err
	}
	region := q.Get("region")
	if region == "" {
		_, region = firstNonEmptyEnv("AWS_REGION", "AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}
	cfg := aws.Config{
		Region:      region,
		Credentials: creds,
		HTTPClient:  &http.Client{Transport: localTransport()},
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		if q.Get("s3ForcePathStyle") == "true" || q.Get("use_path_style") == "true" {
			o.UsePathStyle = true
		}
	}), nil
}

// gcsEmulatorHost is STORAGE_EMULATOR_HOST, which marks every plunge+gs:// URL as an emulator's.
func gcsEmulatorHost() string { return os.Getenv("STORAGE_EMULATOR_HOST") }

// gcsEndpointKeyError refuses a plunge+gs:// URL with an endpoint query key.
func gcsEndpointKeyError(plungeURL string) error {
	return fmt.Errorf("plunge: %q has an endpoint query key; plunge+gs:// takes a storage emulator from "+
		"STORAGE_EMULATOR_HOST (host:port or a URL) instead, because the stack metadata in the same bucket is "+
		"opened through go-cloud, which follows only that variable", plungeURL)
}

// gcsCustomClient is the storage client for a plunge+gs:// URL while STORAGE_EMULATOR_HOST is set: a static token
// from GOOGLE_OAUTH_ACCESS_TOKEN, else no credential. WithoutAuthentication keeps storage.NewClient from looking up
// Application Default Credentials, and the HTTP client carries the token, so nothing here fetches one.
func gcsCustomClient(ctx context.Context, plungeURL string) (*storage.Client, error) {
	base := localTransport()
	hc := &http.Client{Transport: base}
	if tok := os.Getenv("GOOGLE_OAUTH_ACCESS_TOKEN"); tok != "" {
		hc = &http.Client{Transport: &oauth2.Transport{
			Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: tok}), Base: base,
		}}
	}
	client, err := storage.NewClient(ctx, option.WithoutAuthentication(), option.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("plunge: building the GCS client for %q against STORAGE_EMULATOR_HOST %s: %w",
			plungeURL, gcsEmulatorHost(), err)
	}
	return client, nil
}

// refuseDefaultChains refuses, before anything is opened, a plunge URL whose stack metadata URL (diyURL: the URL
// inside it, or plunge+k8s://'s metadata key) names a custom endpoint while the environment would leave the DIY
// backend's go-cloud bucket to a default credential chain. It reads only the URL and the environment.
func refuseDefaultChains(plungeURL, diyURL string) error {
	u, err := url.Parse(diyURL)
	if err != nil {
		return fmt.Errorf("plunge: parsing %q: %w", diyURL, err)
	}
	q := u.Query()
	switch u.Scheme {
	case "s3":
		if endpoint := s3CustomEndpoint(q); endpoint != "" {
			if _, err := s3StaticCredentials(plungeURL, endpoint, q); err != nil {
				return err
			}
		}
	case "gs":
		if q.Has("endpoint") {
			return gcsEndpointKeyError(plungeURL)
		}
		if host := gcsEmulatorHost(); host != "" {
			if _, v := firstNonEmptyEnv("GOOGLE_OAUTH_ACCESS_TOKEN", "GOOGLE_CREDENTIALS"); v == "" {
				return fmt.Errorf("plunge: %q is opened against STORAGE_EMULATOR_HOST %s, and the stack metadata "+
					"store would look up Application Default Credentials (gcloud's login or the GCE metadata "+
					"server) for it: set GOOGLE_OAUTH_ACCESS_TOKEN (any value an emulator that takes anonymous "+
					"calls accepts) or GOOGLE_CREDENTIALS. With an emulator, plunge never falls back to the "+
					"default credential chain", plungeURL, host)
			}
		}
	}
	return nil
}
