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

// What plunge hands choudoufu's public sweep packages (INTENTIUS/plunge#39, INTENTIUS/choudoufu#2281). The packages
// build no credential and read no marker vocabulary of their own: the caller passes the marker keys, the HTTP client
// and the credential. This file is plunge's side of that seam, as choudoufu's internal/live/sweepconfig is
// choudoufu's. It replaces the plunge.go files the copied gcpsweep and azsweep packages carried.
//
// The endpoint and credential rules stay where they were and run before anything here:
//
//   - GCP: gcpauth.go (INTENTIUS/plunge#42) decides the client for a stack naming custom endpoints, and the Cloud
//     Asset client goes over that client with no credential lookup. Only a stack naming no custom endpoint gets the
//     provider's own chain (gcpsweep.HTTPClient), which is what the provider itself would use against Google.
//   - Azure: a metadata host goes through armendpoints.go (INTENTIUS/plunge#41), and armCustomEndpointSkip turns
//     away every other custom-endpoint stack before armProviderClient is reached, so the default credential chain
//     here is only ever built for one of Azure's public clouds.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/intentius/choudoufu/pkg/azsweep"
	"github.com/intentius/choudoufu/pkg/gcpsweep"
	"github.com/intentius/choudoufu/pkg/markerkeys"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
)

// labelMarkerKeys are plunge's marker keys under the label encoding (Google Cloud labels): plunge-stack and
// plunge-urn, with values carried as markers.LabelValue digests. A caller may pass a want map's values (already
// digests, from markers.For) or the stack and URN as written.
var labelMarkerKeys = markerkeys.Keys{Owner: markers.TagStack, Address: markers.TagURN, Encode: markers.LabelValue}

// gcpAssetClient is the Cloud Asset client for a, over hc when the caller built one (gcpauth.go's client for a stack
// naming custom endpoints, which never looks up a credential) and over the provider's own chain otherwise.
func gcpAssetClient(
	ctx context.Context, a gcpsweep.Attrs, hc *http.Client, creds gcpsweep.Credentials,
) (*gcpsweep.Client, error) {
	if hc == nil {
		var err error
		if hc, creds, err = gcpProviderHTTPClient(ctx, a); err != nil {
			return nil, err
		}
	}
	return gcpsweep.New(ctx, a, gcpsweep.Config{Markers: labelMarkerKeys, HTTPClient: hc, Credentials: creds})
}

// gcpProviderHTTPClient is the google provider's own credential chain for a, for a stack that names no custom
// endpoint. gcpLocalAuthFor has already taken every stack that names one.
func gcpProviderHTTPClient(ctx context.Context, a gcpsweep.Attrs) (*http.Client, gcpsweep.Credentials, error) {
	hc, creds, err := gcpsweep.HTTPClient(ctx, a)
	if err != nil {
		return nil, gcpsweep.Credentials{}, err
	}
	return hc, creds, nil
}

// armProviderClient is the ARM client for an azure provider that names no metadata host and no custom endpoint: a
// client secret when the provider (or ARM_*) names all three of tenant, client and secret, else azidentity's default
// chain, the order the provider tries. Its cloud is one of Azure's named public clouds. A custom endpoint never
// reaches here (armCustomEndpointSkip runs first), and one that did is refused rather than given the default chain.
func armProviderClient(a azsweep.Attrs) (*armClient, error) {
	a = a.WithEnv(os.Getenv)
	if refused := a.RefusedMarks(); len(refused) > 0 {
		return nil, fmt.Errorf("the %s provider sets %s from a sensitive value, which plunge's marker bind does not "+
			"read", a.ProviderName(), strings.Join(refused, " and "))
	}
	if a.MetadataHost != "" || a.ResourceManagerEndpoint != "" || a.ActiveDirectoryAuthorityHost != "" {
		return nil, fmt.Errorf("the %s provider names custom endpoints, which plunge's ARM lister reads only through "+
			"a metadata host; it never sends such a stack to the default credential chain", a.ProviderName())
	}
	var cfg cloud.Configuration
	switch strings.ToLower(a.Environment) {
	case "", "public":
		cfg = cloud.AzurePublic
	case "usgovernment":
		cfg = cloud.AzureGovernment
	case "china":
		cfg = cloud.AzureChina
	default:
		return nil, fmt.Errorf("the %s provider names the Azure environment %q, which plunge's ARM lister does not "+
			"resolve", a.ProviderName(), a.Environment)
	}
	opts := azcore.ClientOptions{Cloud: cfg}
	noDiscovery := a.DisableInstanceDiscovery != nil && *a.DisableInstanceDiscovery
	var cred azcore.TokenCredential
	var err error
	if a.ClientID != "" && a.ClientSecret != "" && a.TenantID != "" {
		cred, err = azidentity.NewClientSecretCredential(a.TenantID, a.ClientID, a.ClientSecret,
			&azidentity.ClientSecretCredentialOptions{ClientOptions: opts, DisableInstanceDiscovery: noDiscovery})
	} else {
		cred, err = azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
			ClientOptions: opts, TenantID: a.TenantID, DisableInstanceDiscovery: noDiscovery,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("building the %s credential for plunge's marker bind: %w", a.ProviderName(), err)
	}
	rm := cfg.Services[cloud.ResourceManager]
	return &armClient{endpoint: strings.TrimSuffix(rm.Endpoint, "/"), scope: armScope(rm.Audience), cred: cred}, nil
}
