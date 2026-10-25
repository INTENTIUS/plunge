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

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/pulumi/pulumi/pkg/v3/backend/diy"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/staterecord"
)

// URLPrefix marks a plunge backend URL: "plunge+" ahead of a DIY backend URL. The DIY URL after it keeps the
// stack metadata (which stacks exist, their tags and history) exactly where the DIY backend keeps it; the records
// live beside it under ".pulumi/records".
//
//	plunge+file:///path/to/dir        local records, one host only
//	plunge+s3://bucket/prefix         S3 conditional writes (region, profile, endpoint, s3ForcePathStyle query keys)
//	plunge+azblob://container/prefix  Azure Blob ETag conditions (storage_account query key or AZURE_STORAGE_ACCOUNT)
//	plunge+gs://bucket/prefix         GCS generation preconditions
const URLPrefix = "plunge+"

// recordsDir is where the records live relative to the DIY backend root.
const recordsDir = ".pulumi/records"

// IsPlungeBackendURL reports whether u is a plunge backend URL with a DIY URL scheme this build supports.
func IsPlungeBackendURL(u string) bool {
	inner, ok := strings.CutPrefix(u, URLPrefix)
	if !ok {
		return false
	}
	scheme, _, _ := strings.Cut(inner, "://")
	switch scheme {
	case "file", "s3", "azblob", "gs":
		return diy.IsDIYBackendURL(inner)
	}
	return false
}

// innerURL is the DIY URL inside a plunge URL.
func innerURL(u string) string { return strings.TrimPrefix(u, URLPrefix) }

// openStore opens the record store a plunge URL names.
func openStore(ctx context.Context, plungeURL string) (staterecord.Store, error) {
	inner := innerURL(plungeURL)
	u, err := url.Parse(inner)
	if err != nil {
		return nil, fmt.Errorf("plunge: parsing %q: %w", plungeURL, err)
	}
	q := u.Query()
	prefix := strings.Trim(u.Path, "/")
	keyPrefix := recordsDir
	if prefix != "" {
		keyPrefix = prefix + "/" + recordsDir
	}

	switch u.Scheme {
	case "file":
		dir := strings.TrimPrefix(inner, "file://")
		if i := strings.IndexByte(dir, '?'); i >= 0 {
			dir = dir[:i]
		}
		if dir == "~" || strings.HasPrefix(dir, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("plunge: resolving ~ in %q: %w", plungeURL, err)
			}
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
		return staterecord.NewLocalStore(filepath.Join(dir, filepath.FromSlash(recordsDir)))

	case "s3":
		var opts []func(*awsconfig.LoadOptions) error
		if r := q.Get("region"); r != "" {
			opts = append(opts, awsconfig.WithRegion(r))
		}
		if p := q.Get("profile"); p != "" {
			opts = append(opts, awsconfig.WithSharedConfigProfile(p))
		}
		cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("plunge: loading the AWS configuration for %q: %w", plungeURL, err)
		}
		client := s3.NewFromConfig(cfg, func(o *s3.Options) {
			if e := q.Get("endpoint"); e != "" {
				o.BaseEndpoint = aws.String(e)
			}
			if q.Get("s3ForcePathStyle") == "true" || q.Get("use_path_style") == "true" {
				o.UsePathStyle = true
			}
		})
		return staterecord.NewS3Store(staterecord.S3Config{Client: client, Bucket: u.Host, KeyPrefix: keyPrefix})

	case "azblob":
		account := q.Get("storage_account")
		if account == "" {
			account = os.Getenv("AZURE_STORAGE_ACCOUNT")
		}
		if account == "" {
			return nil, fmt.Errorf("plunge: %q names no storage account: set the storage_account query key "+
				"or AZURE_STORAGE_ACCOUNT", plungeURL)
		}
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("plunge: building an Azure credential for %q: %w", plungeURL, err)
		}
		endpoint := fmt.Sprintf("https://%s.blob.core.windows.net/%s", account, u.Host)
		client, err := container.NewClient(endpoint, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("plunge: building the container client for %q: %w", plungeURL, err)
		}
		return staterecord.NewAzureBlobStore(staterecord.AzureBlobConfig{
			Container:           client,
			Account:             account,
			ContainerName:       u.Host,
			KeyPrefix:           keyPrefix,
			ContractUnavailable: "plunge does not read the storage account's settings",
		})

	case "gs":
		client, err := storage.NewClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("plunge: building the GCS client for %q: %w", plungeURL, err)
		}
		return staterecord.NewGCSStore(staterecord.GCSConfig{Client: client, Bucket: u.Host, KeyPrefix: keyPrefix})
	}
	return nil, fmt.Errorf("plunge: %q is not a plunge backend URL (want %sfile://, %ss3://, %sazblob:// or %sgs://)",
		plungeURL, URLPrefix, URLPrefix, URLPrefix, URLPrefix)
}
