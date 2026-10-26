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

// The plunge+azblob:// store's connection (INTENTIUS/plunge#35). With no endpoint or metadata_host query key it is
// real Azure: https://<account>.blob.core.windows.net and azidentity's default credential chain. With either, the
// store talks to the endpoints the URL names (an emulator such as floci-az) and signs with a credential the
// environment names for it, and it never falls back to the shell's Azure login, the way choudoufu's
// record_store "azurerm" reaches floci-az (internal/live/projection/azurerm_store.go):
//
//   - endpoint is the account's blob service URL ("https://<account>.blob.localhost:4577", floci-az's form under
//     FLOCI_AZ_STORAGE_SUFFIX=localhost:4577); the container is appended to it.
//   - metadata_host is a host serving /metadata/endpoints, as the azure provider's metadataHost is: it names the
//     storage suffix (the blob URL when endpoint is unset), ARM and the login endpoint, which the settings check
//     (INTENTIUS/plunge#20) reads the account through.
//
// The blobs are signed with the account's shared key (ARM_ACCESS_KEY or AZURE_STORAGE_KEY), else a SAS
// (ARM_SAS_TOKEN or AZURE_STORAGE_SAS_TOKEN), else a client secret (ARM_TENANT_ID, ARM_CLIENT_ID and
// ARM_CLIENT_SECRET, or their AZURE_ names) against the metadata host's login endpoint. The secrets are read from the
// environment only, since a backend URL is written to the CLI's credentials file and shown in output. The settings
// check needs ARM, so it needs metadata_host and the client secret; without them each setting is reported
// unreadable with the reason, which refuses the open unless allow_insecure names the settings.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

// azureStoreTransport is what a plunge+azblob:// store with an endpoint or metadata_host sends its requests over:
// the metadata read, the token requests, ARM and the blobs. Nil is http.DefaultClient. Tests set it to a client that
// fails on any host that is not the loopback address.
var azureStoreTransport policy.Transporter

func azureTransport() policy.Transporter {
	if azureStoreTransport != nil {
		return azureStoreTransport
	}
	return http.DefaultClient
}

// azureMetadata is the part of a metadata host's /metadata/endpoints the store reads.
type azureMetadata struct {
	Authentication struct {
		LoginEndpoint string   `json:"loginEndpoint"`
		Audiences     []string `json:"audiences"`
	} `json:"authentication"`
	ResourceManager string            `json:"resourceManager"`
	Suffixes        map[string]string `json:"suffixes"`
}

// readAzureMetadata reads host's /metadata/endpoints over TLS, as azurerm and choudoufu do.
func readAzureMetadata(ctx context.Context, host string) (*azureMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://"+host+"/metadata/endpoints?api-version=2023-11-01", nil)
	if err != nil {
		return nil, err
	}
	resp, err := azureTransport().Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading the endpoints from the metadata host %s: %w", host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the metadata host %s answered HTTP %d", host, resp.StatusCode)
	}
	var md azureMetadata
	if err := json.NewDecoder(resp.Body).Decode(&md); err != nil {
		return nil, fmt.Errorf("decoding the metadata host %s's endpoints: %w", host, err)
	}
	if md.Suffixes["storage"] == "" || md.ResourceManager == "" || md.Authentication.LoginEndpoint == "" ||
		len(md.Authentication.Audiences) == 0 {
		return nil, fmt.Errorf("the metadata host %s names no storage suffix, resource manager, login endpoint "+
			"or audience", host)
	}
	return &md, nil
}

func firstNonEmptyEnv(names ...string) (string, string) {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return n, v
		}
	}
	return "", ""
}

// azureCustomStore is the connection of a plunge+azblob:// URL with an endpoint or metadata_host query key: the
// container client and, when the settings can be read, the ARM client factory, else the reason they cannot.
func azureCustomStore(
	ctx context.Context, plungeURL, account, containerName, subscription string, q url.Values,
) (*container.Client, *armstorage.ClientFactory, string, error) {
	endpoint := strings.TrimSuffix(q.Get("endpoint"), "/")
	host := q.Get("metadata_host")
	var md *azureMetadata
	if host != "" {
		var err error
		if md, err = readAzureMetadata(ctx, host); err != nil {
			return nil, nil, "", fmt.Errorf("plunge: %q: %w", plungeURL, err)
		}
		if endpoint == "" {
			endpoint = fmt.Sprintf("https://%s.blob.%s", account, md.Suffixes["storage"])
		}
	}
	if u, err := url.Parse(endpoint); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, nil, "", fmt.Errorf("plunge: the endpoint query key of %q is %q, which is not an http(s) URL",
			plungeURL, endpoint)
	}
	containerURL := endpoint + "/" + url.PathEscape(containerName)
	opts := policy.ClientOptions{Transport: azureTransport(), Telemetry: policy.TelemetryOptions{Disabled: true}}

	// The token credential, for the blobs when no key or SAS is given and for the settings check.
	var cred azcore.TokenCredential
	credWhy := ""
	_, tenant := firstNonEmptyEnv("ARM_TENANT_ID", "AZURE_TENANT_ID")
	_, client := firstNonEmptyEnv("ARM_CLIENT_ID", "AZURE_CLIENT_ID")
	_, secret := firstNonEmptyEnv("ARM_CLIENT_SECRET", "AZURE_CLIENT_SECRET")
	var cloudCfg cloud.Configuration
	switch {
	case md == nil:
		credWhy = "the URL names no metadata_host, which says where ARM and the login endpoint are"
	case tenant == "" || client == "" || secret == "":
		credWhy = "ARM_TENANT_ID, ARM_CLIENT_ID and ARM_CLIENT_SECRET are not all set"
	default:
		audience := md.Authentication.Audiences[0]
		cloudCfg = cloud.Configuration{
			ActiveDirectoryAuthorityHost: md.Authentication.LoginEndpoint,
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: md.ResourceManager, Audience: audience},
			},
		}
		credOpts := opts
		credOpts.Cloud = cloudCfg
		c, err := azidentity.NewClientSecretCredential(tenant, client, secret, &azidentity.ClientSecretCredentialOptions{
			ClientOptions: credOpts,
			// Instance discovery asks login.microsoftonline.com about the authority; the metadata host is it.
			DisableInstanceDiscovery: true,
		})
		if err != nil {
			return nil, nil, "", fmt.Errorf("plunge: building the client secret credential for %q: %w", plungeURL, err)
		}
		cred = c
	}

	var cc *container.Client
	var err error
	if keyVar, key := firstNonEmptyEnv("ARM_ACCESS_KEY", "AZURE_STORAGE_KEY"); key != "" {
		shared, kerr := container.NewSharedKeyCredential(account, key)
		if kerr != nil {
			return nil, nil, "", fmt.Errorf("plunge: %s is not a storage account key: %w", keyVar, kerr)
		}
		cc, err = container.NewClientWithSharedKeyCredential(containerURL, shared, &container.ClientOptions{ClientOptions: opts})
	} else if _, sas := firstNonEmptyEnv("ARM_SAS_TOKEN", "AZURE_STORAGE_SAS_TOKEN"); sas != "" {
		cc, err = container.NewClientWithNoCredential(containerURL+"?"+strings.TrimPrefix(sas, "?"),
			&container.ClientOptions{ClientOptions: opts})
	} else if cred != nil {
		blobOpts := opts
		blobOpts.Cloud = cloudCfg
		cc, err = container.NewClient(containerURL, cred, &container.ClientOptions{
			ClientOptions: blobOpts, Audience: md.Authentication.Audiences[0],
		})
	} else {
		return nil, nil, "", fmt.Errorf("plunge: %q names a custom Azure endpoint and no credential for it: set "+
			"ARM_ACCESS_KEY (the account key), ARM_SAS_TOKEN, or a metadata_host with ARM_TENANT_ID, ARM_CLIENT_ID "+
			"and ARM_CLIENT_SECRET. With an endpoint given, plunge never falls back to the shell's Azure login (%s)",
			plungeURL, credWhy)
	}
	if err != nil {
		return nil, nil, "", fmt.Errorf("plunge: building the container client for %q: %w", plungeURL, err)
	}
	if cred == nil {
		return cc, nil, fmt.Sprintf("the account's settings are read through ARM, and %s; an access key or a SAS "+
			"reaches the blobs and not the account's settings", credWhy), nil
	}
	if subscription == "" {
		return cc, nil, "", nil
	}
	armOpts := opts
	armOpts.Cloud = cloudCfg
	factory, err := armstorage.NewClientFactory(subscription, cred, &arm.ClientOptions{
		ClientOptions: armOpts,
		// A read of three settings registers nothing.
		DisableRPRegistration: true,
	})
	if err != nil {
		return nil, nil, "", fmt.Errorf("plunge: building the storage management client for %q: %w", plungeURL, err)
	}
	return cc, factory, "", nil
}

// hasAzureCustomEndpoint reports whether a plunge+azblob:// URL's query names an endpoint or a metadata host.
func hasAzureCustomEndpoint(q url.Values) bool {
	return q.Get("endpoint") != "" || q.Get("metadata_host") != ""
}
