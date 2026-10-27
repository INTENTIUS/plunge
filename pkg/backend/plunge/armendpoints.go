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

// The ARM lister's connection when the stack's azure provider reads its endpoints from a metadata host
// (INTENTIUS/plunge#41), as floci-az serves them. The metadata host's /metadata/endpoints (the same read the
// plunge+azblob:// store makes, azblobstore.go) names ARM, the login endpoint and the token audience, and the lister
// lists through them.
//
// The credential is a client secret from the environment (ARM_TENANT_ID, ARM_CLIENT_ID and ARM_CLIENT_SECRET, or
// their AZURE_ names), signed in at the metadata host's login endpoint with instance discovery off. It is never
// azidentity's default chain: with a metadata host set, the shell's Azure login (the Azure CLI, a managed identity,
// workload identity) would mint a token from the public cloud's login. The secret is read from the environment only,
// the rule INTENTIUS/plunge#35 made for the azblob store; the provider's clientSecret input is not read on this path.
// Without the three variables the listing is skipped with a warning naming the stack.
//
// INTENTIUS/plunge#27's rule holds as well: when the metadata host is a loopback host, every endpoint it names must
// be one too, or the listing is skipped rather than sent to a host that is not local.
//
// This file is plunge's own and uses nothing from the copied azsweep package, so INTENTIUS/plunge#39's switch to
// choudoufu's public package leaves it as it is.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// listerAzureTransport is what the ARM lister's metadata read, token requests and list calls go over: listerTransport
// when a test sets it, else http.DefaultClient.
func listerAzureTransport() policy.Transporter {
	if listerTransport != nil {
		return &http.Client{Transport: listerTransport}
	}
	return http.DefaultClient
}

// isLoopbackHost reports whether host (a host, host:port or URL) names the loopback address or localhost.
func isLoopbackHost(host string) bool {
	if u, err := url.Parse(host); err == nil && u.Host != "" {
		host = u.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// armScope is the token scope for an ARM audience: the audience with "/.default", after a trailing slash is dropped.
func armScope(audience string) string {
	return strings.TrimSuffix(audience, "/") + "/.default"
}

// armMetadataHostClient is the ARM client for a provider reading its endpoints from host, or the skip that says why
// there is none. Nothing is sent before the credential check passes, so a skipped listing makes no request at all.
func armMetadataHostClient(ctx context.Context, urn resource.URN, host string) (*armClient, *SkippedListing, error) {
	_, tenant := firstNonEmptyEnv("ARM_TENANT_ID", "AZURE_TENANT_ID")
	_, client := firstNonEmptyEnv("ARM_CLIENT_ID", "AZURE_CLIENT_ID")
	_, secret := firstNonEmptyEnv("ARM_CLIENT_SECRET", "AZURE_CLIENT_SECRET")
	if tenant == "" || client == "" || secret == "" {
		return nil, skipped(urn, "ARM", "the azure provider takes its endpoints from the metadata host %s, and "+
			"ARM_TENANT_ID, ARM_CLIENT_ID and ARM_CLIENT_SECRET are not all set; with a metadata host plunge signs in "+
			"with a client secret from the environment only, never the shell's Azure login", host), nil
	}

	transport := listerAzureTransport()
	md, err := fetchAzureMetadata(ctx, host, transport)
	if err != nil {
		return nil, nil, err
	}
	if md.ResourceManager == "" || md.Authentication.LoginEndpoint == "" || len(md.Authentication.Audiences) == 0 {
		return nil, skipped(urn, "ARM", "the metadata host %s names no resource manager, login endpoint or "+
			"audience", host), nil
	}
	if isLoopbackHost(host) {
		for _, ep := range []struct{ name, url string }{
			{"resource manager", md.ResourceManager},
			{"login endpoint", md.Authentication.LoginEndpoint},
		} {
			if !isLoopbackHost(ep.url) {
				return nil, skipped(urn, "ARM", "the metadata host %s is local and names the %s %s, which is not",
					host, ep.name, ep.url), nil
			}
		}
	}

	audience := md.Authentication.Audiences[0]
	opts := azcore.ClientOptions{
		Transport: transport,
		Telemetry: policy.TelemetryOptions{Disabled: true},
		Cloud: cloud.Configuration{
			ActiveDirectoryAuthorityHost: md.Authentication.LoginEndpoint,
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: md.ResourceManager, Audience: audience},
			},
		},
	}
	cred, err := azidentity.NewClientSecretCredential(tenant, client, secret, &azidentity.ClientSecretCredentialOptions{
		ClientOptions: opts,
		// Instance discovery asks login.microsoftonline.com about the authority; the metadata host is it.
		DisableInstanceDiscovery: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("building the client secret credential for the metadata host %s: %w", host, err)
	}
	return &armClient{
		endpoint: strings.TrimSuffix(md.ResourceManager, "/"),
		scope:    armScope(audience),
		cred:     cred,
		do:       transport,
	}, nil, nil
}

// armCustomEndpointSkip is the skip for an azure provider that names custom endpoints other than a metadata host
// (environment "custom", or azapi's ARM_RESOURCE_MANAGER_ENDPOINT and ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST): the
// lister resolves only a metadata host, and it never reaches the default credential chain or the public cloud for
// such a stack. Nil when there is none.
func armCustomEndpointSkip(urn resource.URN, inputs resource.PropertyMap) *SkippedListing {
	var named []string
	env := providerString(inputs, "environment")
	if env == "" {
		env = firstEnv("ARM_ENVIRONMENT", "AZURE_ENVIRONMENT")
	}
	if strings.EqualFold(env, "custom") {
		named = append(named, `environment "custom"`)
	}
	for _, n := range []string{"ARM_RESOURCE_MANAGER_ENDPOINT", "ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST"} {
		if firstEnv(n) != "" {
			named = append(named, n)
		}
	}
	if len(named) == 0 {
		return nil
	}
	return skipped(urn, "ARM", "the azure provider names custom endpoints (%s) and no metadata host, which is the "+
		"only form plunge's ARM lister resolves (set metadataHost or ARM_METADATA_HOSTNAME)", strings.Join(named, ", "))
}
