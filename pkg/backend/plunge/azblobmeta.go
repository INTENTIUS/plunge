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

// The stack metadata half of an azblob:// URL (the URL inside plunge+azblob://, or plunge+k8s://'s metadata key),
// a follow-up to INTENTIUS/plunge#50. diy.New, an upstream file, opens it through go-cloud's azureblob driver
// (gocloud.dev/blob/azureblob), with azureblob.NewDefaultClient and azureblob.NewDefaultServiceURLOptions:
//
//   - The service URL is https://<account>.blob.core.windows.net unless the URL's domain, protocol, cdn or
//     localemu query keys, or AZURE_STORAGE_DOMAIN, AZURE_STORAGE_PROTOCOL, AZURE_STORAGE_IS_CDN or
//     AZURE_STORAGE_IS_LOCAL_EMULATOR, move it. The account is the storage_account key, else
//     AZURE_STORAGE_ACCOUNT. A domain starting with 127.0.0.1 or localhost, or localemu, puts the account in the
//     path; cdn drops it.
//   - Any other query key refuses the open ("unknown query parameter"), so a URL with plunge's endpoint,
//     metadata_host, resource_group, subscription_id or allow_insecure key never opened through diy.New at all.
//   - The credential is, in order: the shared key when AZURE_STORAGE_ACCOUNT and AZURE_STORAGE_KEY are both set;
//     no credential with AZURE_STORAGE_SAS_TOKEN appended to the service URL; AZURE_STORAGE_CONNECTION_STRING (or
//     AZURE_STORAGEBLOB_CONNECTIONSTRING), whose own BlobEndpoint replaces the service URL; else
//     azidentity.NewDefaultAzureCredential, which can reach the instance metadata service (169.254.169.254),
//     login.microsoftonline.com or the shell's az login.
//
// azblobMetadataURL rewrites the URL before diy.New so the two halves agree. plunge's own keys are dropped. With an
// endpoint or metadata_host key, the service URL is spelled in go-cloud's keys so that it is exactly the record
// store's endpoint, and the open is refused by name when that endpoint is not local (localhost, *.localhost or a
// loopback address), or when go-cloud would pick a connection string or the default credential. ARM_ACCESS_KEY and
// ARM_SAS_TOKEN reach the record store only: go-cloud reads the AZURE_STORAGE_ names. Without either key, go-cloud's
// own emulator keys and variables are refused, since the record store would stay on real Azure while the stack
// metadata went elsewhere.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"gocloud.dev/blob/azureblob"
)

// azblobPlungeKeys are the query keys plunge reads on an azblob URL and go-cloud's azureblob opener does not know.
var azblobPlungeKeys = []string{"endpoint", "metadata_host", "resource_group", "subscription_id", "allow_insecure"}

// goCloudAzblobKeys are go-cloud's azureblob query keys that move the service URL.
var goCloudAzblobKeys = []string{"domain", "protocol", "cdn", "localemu"}

// goCloudAzblobEnv are the variables go-cloud's azureblob opener reads that move the service URL.
var goCloudAzblobEnv = []string{
	"AZURE_STORAGE_DOMAIN", "AZURE_STORAGE_PROTOCOL", "AZURE_STORAGE_IS_CDN", "AZURE_STORAGE_IS_LOCAL_EMULATOR",
}

// isAzblobURL reports whether a plunge URL names the Azure Blob record store.
func isAzblobURL(u string) bool { return strings.HasPrefix(innerURL(u), "azblob://") }

// goCloudAzureCredential is the credential go-cloud's azureblob.NewDefaultClient takes from the environment, in its
// order (newCredInfoFromEnv in gocloud.dev/blob/azureblob): "shared key", "sas", "connection string" or "default".
func goCloudAzureCredential() string {
	switch {
	case os.Getenv("AZURE_STORAGE_ACCOUNT") != "" && os.Getenv("AZURE_STORAGE_KEY") != "":
		return "shared key"
	case os.Getenv("AZURE_STORAGE_SAS_TOKEN") != "":
		return "sas"
	}
	if _, v := firstNonEmptyEnv("AZURE_STORAGE_CONNECTION_STRING", "AZURE_STORAGEBLOB_CONNECTIONSTRING"); v != "" {
		return "connection string"
	}
	return "default"
}

// azblobMetadataURL is the DIY URL diy.New opens for a plunge URL's stack metadata (diyURL, from metadataURL). A URL
// that is not azblob:// is returned as given. An azblob:// URL loses plunge's own query keys, and with an endpoint or
// metadata_host key it gets go-cloud's keys for the same endpoint, or a refusal naming what would leave the machine.
// It reads the URL and the environment, and for a metadata_host with no endpoint the host's /metadata/endpoints.
func azblobMetadataURL(ctx context.Context, plungeURL, diyURL string) (string, error) {
	u, err := url.Parse(diyURL)
	if err != nil {
		return "", fmt.Errorf("plunge: parsing %q: %w", diyURL, err)
	}
	if u.Scheme != "azblob" {
		return diyURL, nil
	}
	q := u.Query()
	endpoint := strings.TrimSuffix(q.Get("endpoint"), "/")
	host := q.Get("metadata_host")
	for _, k := range goCloudAzblobKeys {
		if q.Has(k) {
			return "", fmt.Errorf("plunge: %q has go-cloud's %s query key, which only the stack metadata store "+
				"reads: the record store would stay on real Azure. Name an emulator with the endpoint key (the "+
				"account's blob service URL), which both halves use", plungeURL, k)
		}
	}
	for _, k := range azblobPlungeKeys {
		q.Del(k)
	}
	if endpoint == "" && host == "" {
		if n, v := firstNonEmptyEnv(goCloudAzblobEnv...); v != "" {
			return "", fmt.Errorf("plunge: %s is set (%q), which moves the stack metadata of %q off real Azure "+
				"while its records stay there. Unset it and name the emulator with the endpoint query key",
				n, v, plungeURL)
		}
		u.RawQuery = q.Encode()
		return u.String(), nil
	}

	account := q.Get("storage_account")
	if account == "" {
		account = os.Getenv("AZURE_STORAGE_ACCOUNT")
	}
	if account == "" {
		return "", fmt.Errorf("plunge: %q names no storage account: set the storage_account query key "+
			"or AZURE_STORAGE_ACCOUNT", plungeURL)
	}
	if endpoint == "" {
		// The record store derives the blob URL from the metadata host's storage suffix (azureCustomStore); so does
		// this, after checking the host is local.
		if !isLoopbackHost(host) {
			return "", fmt.Errorf("plunge: the metadata_host of %q is %s, which is not local (localhost, "+
				"*.localhost or a loopback address); the stack metadata is opened only against a local emulator",
				plungeURL, host)
		}
		md, err := fetchAzureMetadata(ctx, host, azureTransport())
		if err != nil {
			return "", fmt.Errorf("plunge: %q: %w", plungeURL, err)
		}
		if md.Suffixes["storage"] == "" {
			return "", fmt.Errorf("plunge: %q: the metadata host %s names no storage suffix", plungeURL, host)
		}
		endpoint = fmt.Sprintf("https://%s.blob.%s", account, md.Suffixes["storage"])
	}
	e, err := url.Parse(endpoint)
	if err != nil || e.Host == "" || (e.Scheme != "https" && e.Scheme != "http") || e.RawQuery != "" ||
		e.Fragment != "" {
		return "", fmt.Errorf("plunge: the endpoint of %q is %q, which is not an http(s) URL with no query",
			plungeURL, endpoint)
	}
	if !isLoopbackHost(e.Host) {
		return "", fmt.Errorf("plunge: the endpoint of %q is %s, which is not local (localhost, *.localhost or a "+
			"loopback address). The stack metadata goes through go-cloud's azureblob driver, whose requests "+
			"plunge cannot route, so plunge opens it only against a local emulator", plungeURL, e.Host)
	}

	switch goCloudAzureCredential() {
	case "shared key":
		if a := os.Getenv("AZURE_STORAGE_ACCOUNT"); a != account {
			return "", fmt.Errorf("plunge: %q names the storage account %s, and AZURE_STORAGE_ACCOUNT is %s; the "+
				"stack metadata store signs with AZURE_STORAGE_KEY as AZURE_STORAGE_ACCOUNT, so the two must match",
				plungeURL, account, a)
		}
	case "sas":
	case "connection string":
		return "", fmt.Errorf("plunge: %q names a custom Azure endpoint, and the stack metadata store would sign "+
			"with AZURE_STORAGE_CONNECTION_STRING, whose own BlobEndpoint replaces the endpoint. Set "+
			"AZURE_STORAGE_ACCOUNT with AZURE_STORAGE_KEY, or AZURE_STORAGE_SAS_TOKEN, instead", plungeURL)
	default:
		return "", fmt.Errorf("plunge: %q names a custom Azure endpoint and no credential the stack metadata "+
			"store reads: set AZURE_STORAGE_ACCOUNT and AZURE_STORAGE_KEY (the account key), or "+
			"AZURE_STORAGE_SAS_TOKEN. The store goes through go-cloud, which reads those names and not ARM_ACCESS_KEY "+
			"or ARM_SAS_TOKEN, and with neither it would build Azure's default credential chain (managed identity, "+
			"login.microsoftonline.com, the az login), which plunge never does against an endpoint", plungeURL)
	}

	keys, ok := goCloudServiceKeys(account, e)
	if !ok {
		return "", fmt.Errorf("plunge: the endpoint of %q is %s, which go-cloud's azureblob driver (the stack "+
			"metadata store) cannot address: it puts the account in the path for a 127.0.0.1 or localhost host. "+
			"Name the endpoint as %s://%s/%s or %s://%s.blob.localhost:<port>",
			plungeURL, endpoint, e.Scheme, e.Host, account, e.Scheme, account)
	}
	for k, v := range keys {
		q.Set(k, v)
	}
	q.Set("storage_account", account)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// goCloudServiceKeys are go-cloud's azureblob query keys (domain, protocol, cdn, localemu) that make its service URL
// exactly e, checked with azureblob.NewServiceURL itself. All four are set, so go-cloud's environment variables for
// them are overridden. ok is false when no spelling gives e.
func goCloudServiceKeys(account string, e *url.URL) (map[string]string, bool) {
	want := e.Scheme + "://" + e.Host + strings.TrimSuffix(e.Path, "/")
	type spelling struct {
		domain        string
		cdn, localemu bool
	}
	var tries []spelling
	if d, ok := strings.CutPrefix(e.Host, account+"."); ok && strings.TrimSuffix(e.Path, "/") == "" {
		tries = append(tries, spelling{domain: d}) // <protocol>://<account>.<domain>
	}
	if strings.TrimSuffix(e.Path, "/") == "/"+account {
		tries = append(tries, spelling{domain: e.Host, localemu: true}) // <protocol>://<domain>/<account>
	}
	tries = append(tries, spelling{domain: e.Host + strings.TrimSuffix(e.Path, "/"), cdn: true}) // <protocol>://<domain>
	for _, s := range tries {
		svc, err := azureblob.NewServiceURL(&azureblob.ServiceURLOptions{
			AccountName: account, StorageDomain: s.domain, Protocol: e.Scheme, IsCDN: s.cdn, IsLocalEmulator: s.localemu,
		})
		if err == nil && string(svc) == want {
			return map[string]string{
				"domain":   s.domain,
				"protocol": e.Scheme,
				"cdn":      fmt.Sprint(s.cdn),
				"localemu": fmt.Sprint(s.localemu),
			}, true
		}
	}
	return nil, false
}
