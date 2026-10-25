// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// Credentials is how a client was told to authenticate, so that a failure
// can be reported against what was actually used. It never holds the
// credential itself.
type Credentials struct {
	// Method is the credential chain's choice, in its own words: "Client
	// Secret Auth", "Azure CLI Auth", "Managed Service Identity Auth".
	// Empty for a [NewWith] client, which was handed a credential.
	Method string
	// TenantID and ClientID are the block's, when it names them.
	TenantID string
	ClientID string
}

// String is the one line a diagnostic names the credential with.
func (c Credentials) String() string {
	if c.Method == "" {
		return "the configured credential"
	}
	s := c.Method
	if c.ClientID != "" {
		s += " as client " + c.ClientID
	}
	if c.TenantID != "" {
		s += " in tenant " + c.TenantID
	}
	return s
}

// Endpoints are where a client sends its query and what it asks a token
// for.
type Endpoints struct {
	// ResourceManager is the ARM base URL: https://management.azure.com/
	// for the public cloud.
	ResourceManager string
	// Audience is the resource a token is requested for; the scope asked
	// for is Audience + "/.default".
	Audience string
}

// cloudConfig is the cloud the block names. plunge carries no copy of
// OpenTofu's azurerm backend credential package, so the cloud comes from
// the azcore presets by name, overridden field by field by azapi's
// endpoint values as the providers apply them; metadata_host is refused,
// since resolving it needs that package.
func (a Attrs) cloudConfig(ctx context.Context) (cloud.Configuration, error) {
	var cfg cloud.Configuration
	if a.MetadataHost != "" {
		return cloud.Configuration{}, fmt.Errorf("metadata_host %q is not read by plunge's Azure lister; name the cloud with environment instead", a.MetadataHost)
	}
	switch strings.ToLower(a.Environment) {
	case "", "public":
		cfg = cloud.AzurePublic
	case "usgovernment":
		cfg = cloud.AzureGovernment
	case "china":
		cfg = cloud.AzureChina
	case "custom":
		var missing []string
		for _, f := range []struct{ name, val string }{
			{"active_directory_authority_host", a.ActiveDirectoryAuthorityHost},
			{"resource_manager_endpoint", a.ResourceManagerEndpoint},
			{"resource_manager_audience", a.ResourceManagerAudience},
		} {
			if f.val == "" {
				missing = append(missing, f.name)
			}
		}
		if len(missing) > 0 {
			return cloud.Configuration{}, fmt.Errorf("environment \"custom\" needs %s", strings.Join(missing, ", "))
		}
		cfg = cloud.Configuration{
			ActiveDirectoryAuthorityHost: a.ActiveDirectoryAuthorityHost,
			Services:                     map[cloud.ServiceName]cloud.ServiceConfiguration{cloud.ResourceManager: {}},
		}
	default:
		return cloud.Configuration{}, fmt.Errorf("unknown Azure environment %q", a.Environment)
	}
	rm := cfg.Services[cloud.ResourceManager]
	if a.ResourceManagerEndpoint != "" {
		rm.Endpoint = a.ResourceManagerEndpoint
	}
	if a.ResourceManagerAudience != "" {
		rm.Audience = a.ResourceManagerAudience
	}
	services := map[cloud.ServiceName]cloud.ServiceConfiguration{}
	for k, v := range cfg.Services {
		services[k] = v
	}
	services[cloud.ResourceManager] = rm
	cfg.Services = services
	if a.ActiveDirectoryAuthorityHost != "" {
		cfg.ActiveDirectoryAuthorityHost = a.ActiveDirectoryAuthorityHost
	}
	return cfg, nil
}

// New builds a client from a provider's arguments, with the provider's
// environment fallbacks applied ([Attrs.WithEnv]). No token is requested
// until [Client.Search].
func New(ctx context.Context, a Attrs) (*Client, error) {
	a = a.WithEnv(os.Getenv)
	cred, ep, creds, err := a.credential(ctx, "the estate sweep")
	if err != nil {
		return nil, err
	}
	c := NewWith(ep, cred, &http.Client{})
	c.subscription = a.SubscriptionID
	c.creds = creds
	return c, nil
}

// credential is the credential [New] and [NewTagger] share, for arguments
// that already have the environment applied ([Attrs.WithEnv]); who names
// the caller in the errors. A client secret the arguments name is used as
// given; otherwise azidentity's default chain (environment, workload
// identity, managed identity, Azure CLI) is, which is the order the
// providers try when nothing is configured.
func (a Attrs) credential(ctx context.Context, who string) (azcore.TokenCredential, Endpoints, Credentials, error) {
	if refused := a.refusedMarks(); len(refused) > 0 {
		return nil, Endpoints{}, Credentials{}, fmt.Errorf("the %s provider sets %s from a sensitive value, which %s does not read; set it from a non-sensitive value so %s calls as and where the provider does", a.providerName(), strings.Join(refused, " and "), who, who)
	}
	cfg, err := a.cloudConfig(ctx)
	if err != nil {
		return nil, Endpoints{}, Credentials{}, fmt.Errorf("the %s provider's cloud: %w", a.providerName(), err)
	}
	opts := azcore.ClientOptions{Cloud: cfg}
	var cred azcore.TokenCredential
	method := ""
	switch {
	case a.ClientID != "" && a.ClientSecret != "" && a.TenantID != "":
		cred, err = azidentity.NewClientSecretCredential(a.TenantID, a.ClientID, a.ClientSecret,
			&azidentity.ClientSecretCredentialOptions{ClientOptions: opts, DisableInstanceDiscovery: on(a.DisableInstanceDiscovery, false)})
		method = "Client Secret Auth"
	default:
		cred, err = azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
			ClientOptions: opts, TenantID: a.TenantID, DisableInstanceDiscovery: on(a.DisableInstanceDiscovery, false)})
		method = "Default Azure Credential"
	}
	if err != nil {
		return nil, Endpoints{}, Credentials{}, fmt.Errorf("building the %s credential (%s) for %s: %w", a.providerName(), method, who, err)
	}
	rm := cfg.Services[cloud.ResourceManager]
	return cred, Endpoints{ResourceManager: rm.Endpoint, Audience: rm.Audience}, Credentials{Method: method, TenantID: a.TenantID, ClientID: a.ClientID}, nil
}

func (a Attrs) providerName() string {
	if a.Provider == "" {
		return ProviderAzureRM
	}
	return a.Provider
}

// scope is the token scope for an audience: the audience with "/.default"
// appended, the v2 endpoint's form for "every permission this app has on
// that resource". A trailing slash is dropped first, so that the public
// cloud's "https://management.core.windows.net/" asks for
// "https://management.core.windows.net/.default".
func scope(audience string) string {
	return strings.TrimSuffix(audience, "/") + "/.default"
}
