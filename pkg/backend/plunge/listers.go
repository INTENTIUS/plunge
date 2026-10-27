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

// The per-family listers bind.go describes. Each reads its connection settings from the provider resource's inputs,
// falling back to the environment variables the provider itself reads, so it calls the cloud as and where the
// provider does. These are ports of how choudoufu's internal/live/discovery drives the same clients
// (cloudcontrol.go, kubernetes.go, gcp.go), reduced to "list one type's objects carrying one URN's markers".

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/azsweep"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/cloudcontrol"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/gcplist"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/gcpsweep"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/kubesweep"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/registry"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// EnvCloudControlEndpoint points the Cloud Control lister at an emulator ("http://localhost:4566"). Unset, the
// lister takes the endpoint from the stack's aws provider configuration (cloudControlEndpoint, endpoints.go).
const EnvCloudControlEndpoint = "PLUNGE_CLOUDCONTROL_ENDPOINT"

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// cloudControlLister lists AWS types through Cloud Control. ListResources reads the service's own store, which is
// the consistent per-type listing choudoufu binds with; the Resource Groups Tagging API is not used here, because its
// index lags.
type cloudControlLister struct {
	clients listerCache[cloudcontrol.Client]
}

func (l *cloudControlLister) Consistent() bool { return true }

func (l *cloudControlLister) Covers(info TypeInfo) (bool, string) {
	if info.CFNType == "" {
		return false, fmt.Sprintf("%s has no CloudFormation counterpart for Cloud Control to list", info.Type)
	}
	if !info.Listable {
		return false, fmt.Sprintf("Cloud Control cannot list %s without a parent's input", info.CFNType)
	}
	if info.Family == FamilyAWS {
		// pulumi-aws's ID is the Terraform id. A single-part Cloud Control identifier is it; a multi-part one is
		// translated through the type's import id grammar (ccid.go).
		if _, reason := awsIDTranslation(info); reason != "" {
			return false, reason
		}
	}
	return true, ""
}

// awsIDTranslation is how a pulumi-aws type's Cloud Control identifiers become Terraform ids: nil for a single-part
// identifier, which is the id as it is.
func awsIDTranslation(info TypeInfo) (*ccTranslation, string) {
	roster, err := registry.Embedded()
	if err != nil {
		return nil, "choudoufu's registry could not be read: " + err.Error()
	}
	switch roster.IdentifierArity(info.CFNType) {
	case 0:
		return nil, fmt.Sprintf("the registry has no primary identifier for %s", info.CFNType)
	case 1:
		return nil, ""
	}
	if t, ok := ccHandTranslation(info.TFType, info.CFNType); ok {
		return t, ""
	}
	t, reason := ccTerraformID(info.TFType, info.CFNType, roster.PrimaryIdentifier(info.CFNType))
	if reason != "" {
		return nil, reason
	}
	return &t, ""
}

func (l *cloudControlLister) ListMarked(
	ctx context.Context, info TypeInfo, req deploy.BindRequest, want map[string]string,
) ([]LiveObject, error) {
	region := providerString(req.ProviderInputs, "region")
	if region == "" {
		region = firstEnv("AWS_REGION", "AWS_DEFAULT_REGION")
	}
	endpoint, skip := cloudControlEndpoint(req.ProviderInputs)
	if skip != "" {
		return nil, skipped(req.URN, "AWS Cloud Control", "%s", skip)
	}
	c, err := l.clients.get(region+"|"+endpoint, func() (*cloudcontrol.Client, error) {
		return cloudcontrol.New(cloudcontrol.Config{Region: region, Endpoint: endpoint, RoundTripper: listerTransport}), nil
	})
	if err != nil {
		return nil, err
	}
	descs, err := c.ListResources(ctx, info.CFNType)
	if err != nil {
		return nil, err
	}
	var translate *ccTranslation
	if info.Family == FamilyAWS {
		var reason string
		if translate, reason = awsIDTranslation(info); reason != "" {
			return nil, errors.New(reason)
		}
	}
	var objs []LiveObject
	for _, d := range descs {
		tags, ok := cfnTags(d.Properties)
		if prop := translate.needsModel(); prop != "" {
			// The id is read from a model property (an ARN), which a list handler may leave out.
			if _, has := d.Properties[prop]; !has {
				ok = false
			}
		}
		if !ok {
			// The list handler left the tags, or the property the id is read from, out: read the one resource.
			full, err := cloudcontrol.GetResourceByIdentity(ctx, c, info.CFNType, d.Identifier)
			if err != nil {
				return nil, err
			}
			if full == nil {
				continue
			}
			tags, _ = cfnTags(full.Properties)
			d.Properties = full.Properties
		}
		id := d.Identifier
		if translate != nil {
			tfID, ok := translate.translate(d.Identifier, d.Properties)
			if !ok {
				return nil, fmt.Errorf("the Cloud Control identifier %q of %s cannot be translated to %s's "+
					"Terraform id (it should have %d parts)", d.Identifier, info.CFNType, info.TFType, translate.arity)
			}
			id = tfID
		}
		objs = append(objs, LiveObject{ID: resource.ID(id), Tags: tags})
	}
	return filterMarked(objs, want), nil
}

// cfnTags reads a CloudFormation model's Tags: a list of {Key, Value} or a map. ok is false when the model has none.
func cfnTags(props map[string]any) (map[string]string, bool) {
	raw, ok := props["Tags"]
	if !ok {
		return nil, false
	}
	out := map[string]string{}
	switch t := raw.(type) {
	case []any:
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				k, _ := m["Key"].(string)
				v, _ := m["Value"].(string)
				out[k] = v
			}
		}
	case map[string]any:
		for k, v := range t {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}
	return out, true
}

// armLister lists Azure resources and resource groups by tag through ARM's own list operations, not Resource Graph
// (azsweep's sweep), whose index lags.
type armLister struct {
	creds listerCache[armClient]
}

type armClient struct {
	endpoint string
	scope    string
	cred     azcore.TokenCredential
}

// armAPIVersion is the Resources API version the list calls are made at.
const armAPIVersion = "2021-04-01"

// Consistent is false: ARM's list consistency is unmeasured (ruled on INTENTIUS/plunge#15, 2026-10-26). An ARM
// listing binds, and its silence is not taken as proof there is no duplicate, until the maintainer's measurement on
// a real subscription says otherwise.
func (l *armLister) Consistent() bool { return false }

// ConsistentFor implements requestConsistency, for the coverage reason the record carries.
func (l *armLister) ConsistentFor(TypeInfo, deploy.BindRequest) (bool, string) {
	return false, "binds, consistency unmeasured: ARM's list consistency has not been measured"
}

func (l *armLister) Covers(info TypeInfo) (bool, string) { return true, "" }

func (l *armLister) ListMarked(
	ctx context.Context, info TypeInfo, req deploy.BindRequest, want map[string]string,
) ([]LiveObject, error) {
	in := req.ProviderInputs
	if host := azureMetadataHost(in); host != "" {
		// The provider reads every endpoint from the metadata host (an emulator such as floci-az). The lister does
		// not resolve one, and without it the call would go to the public cloud's ARM.
		return nil, skipped(req.URN, "ARM", "the azure provider takes its endpoints from the metadata host %s, "+
			"which plunge's ARM lister does not resolve", host)
	}
	sub := providerString(in, "subscriptionId")
	if sub == "" {
		sub = firstEnv("ARM_SUBSCRIPTION_ID", "AZURE_SUBSCRIPTION_ID")
	}
	if sub == "" {
		return nil, fmt.Errorf("no subscription: the provider sets no subscriptionId and ARM_SUBSCRIPTION_ID is unset")
	}
	a := azsweep.Attrs{
		Provider:       azsweep.ProviderAzureRM,
		SubscriptionID: sub,
		TenantID:       providerString(in, "tenantId"),
		ClientID:       providerString(in, "clientId"),
		ClientSecret:   providerString(in, "clientSecret"),
		Environment:    providerString(in, "environment"),
	}
	c, err := l.creds.get(sub+"|"+a.TenantID+"|"+a.ClientID+"|"+a.Environment, func() (*armClient, error) {
		cred, ep, _, err := a.Credential(ctx, "plunge's marker bind")
		if err != nil {
			return nil, err
		}
		return &armClient{endpoint: strings.TrimSuffix(ep.ResourceManager, "/"), scope: azsweep.Scope(ep.Audience),
			cred: cred}, nil
	})
	if err != nil {
		return nil, err
	}
	// ARM filters on one tag; the first URN chunk narrows it, and filterMarked checks every marker.
	filter := fmt.Sprintf("tagName eq '%s' and tagValue eq '%s'", markers.TagURN,
		strings.ReplaceAll(want[markers.TagURN], "'", "''"))
	var objs []LiveObject
	for _, path := range []string{"/resources", "/resourcegroups"} {
		next := c.endpoint + "/subscriptions/" + url.PathEscape(sub) + path + "?api-version=" + armAPIVersion +
			"&$filter=" + url.QueryEscape(filter)
		for next != "" {
			page, err := c.get(ctx, next)
			if err != nil {
				return nil, err
			}
			for _, v := range page.Value {
				objs = append(objs, LiveObject{ID: resource.ID(v.ID), Tags: v.Tags})
			}
			next = page.NextLink
		}
	}
	return filterMarked(objs, want), nil
}

type armPage struct {
	Value []struct {
		ID   string            `json:"id"`
		Tags map[string]string `json:"tags"`
	} `json:"value"`
	NextLink string `json:"nextLink"`
}

func (c *armClient) get(ctx context.Context, u string) (*armPage, error) {
	tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{c.scope}})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ARM answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var page armPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// kubeLister lists one kind by label selector. The API server answers a LIST from a quorum read of etcd unless the
// caller asks for a cached one, which kubesweep never does.
type kubeLister struct {
	clients listerCache[kubeClient]
}

type kubeClient struct {
	sweep *kubesweep.Client
	disc  discovery.DiscoveryInterface
}

func (l *kubeLister) Consistent() bool { return true }

func (l *kubeLister) Covers(info TypeInfo) (bool, string) {
	if _, _, ok := kubeGroupVersionKind(info); !ok {
		return false, fmt.Sprintf("%s does not name a Kubernetes group/version and kind", info.Type)
	}
	return true, ""
}

// kubeGroupVersionKind reads "kubernetes:apps/v1:Deployment" as apps/v1 and Deployment; "core/v1" is "v1".
func kubeGroupVersionKind(info TypeInfo) (string, string, bool) {
	mod := strings.TrimPrefix(string(info.Type.Module()), "kubernetes:")
	kind := string(info.Type.Name())
	if mod == "" || kind == "" || !strings.Contains(mod, "/") {
		return "", "", false
	}
	if strings.HasPrefix(mod, "core/") {
		mod = strings.TrimPrefix(mod, "core/")
	}
	return mod, kind, true
}

func (l *kubeLister) ListMarked(
	ctx context.Context, info TypeInfo, req deploy.BindRequest, want map[string]string,
) ([]LiveObject, error) {
	in := req.ProviderInputs
	a := kubesweep.Attrs{ConfigContext: providerString(in, "context"), ConfigContextCluster: providerString(in, "cluster")}
	kubeconfig := providerString(in, "kubeconfig")
	if kubeconfig != "" && strings.ContainsAny(kubeconfig, "\n{") {
		f, err := os.CreateTemp("", "plunge-kubeconfig-*")
		if err != nil {
			return nil, err
		}
		defer os.Remove(f.Name())
		if _, err := f.WriteString(kubeconfig); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
		kubeconfig = f.Name()
	}
	a.ConfigPath = kubeconfig
	key := kubeconfig + "|" + a.ConfigContext + "|" + a.ConfigContextCluster
	c, err := l.clients.get(key, func() (*kubeClient, error) {
		cfg, err := kubesweep.RestConfig(a)
		if err != nil {
			return nil, err
		}
		sweep, err := kubesweep.New(cfg)
		if err != nil {
			return nil, err
		}
		disc, err := discovery.NewDiscoveryClientForConfig(cfg)
		if err != nil {
			return nil, err
		}
		return &kubeClient{sweep: sweep, disc: disc}, nil
	})
	if err != nil {
		return nil, err
	}
	gv, kind, _ := kubeGroupVersionKind(info)
	list, err := c.disc.ServerResourcesForGroupVersion(gv)
	if err != nil {
		return nil, err
	}
	var k *kubesweep.Kind
	for _, r := range list.APIResources {
		if r.Kind == kind && !strings.Contains(r.Name, "/") {
			parsed, err := schema.ParseGroupVersion(gv)
			if err != nil {
				return nil, err
			}
			k = &kubesweep.Kind{GVR: parsed.WithResource(r.Name), Kind: kind, Namespaced: r.Namespaced, APIVersion: gv}
			break
		}
	}
	if k == nil {
		return nil, fmt.Errorf("the cluster does not serve %s at %s", kind, gv)
	}
	objs, _, err := c.sweep.List(ctx, *k, markers.TagURN, want[markers.TagURN])
	if err != nil {
		return nil, err
	}
	var out []LiveObject
	for _, o := range objs {
		id := o.Name
		if o.Namespace != "" {
			id = o.Namespace + "/" + o.Name
		}
		out = append(out, LiveObject{ID: resource.ID(id), Tags: o.Labels})
	}
	return filterMarked(out, want), nil
}

// gcpLister lists a covered Google Cloud type through its own service's list call (package gcplist). Only Cloud
// Storage, whose bucket listing Google documents as strongly consistent, carries the collision claim; every other
// service's list binds, with its consistency unmeasured (ruled on INTENTIUS/plunge#15, 2026-10-26). A type gcplist
// does not cover, or one whose list cannot be addressed from the resource's inputs and the provider's
// configuration, is searched through Asset Inventory instead (assetLister), which binds but does not carry the
// claim either; the ownership entry says which it was.
type gcpLister struct {
	asset   *assetLister
	clients listerCache[http.Client]
}

func newGCPLister() *gcpLister { return &gcpLister{asset: &assetLister{}} }

func (l *gcpLister) Covers(info TypeInfo) (bool, string) { return true, "" }

// Consistent is false for the family as a whole; ConsistentFor answers per type and request.
func (l *gcpLister) Consistent() bool { return false }

// gcpList is where one request's objects are listed through the type's service, or why they are not.
type gcpList struct {
	t        gcplist.Type
	url      string
	coll     string
	fallback string
	// skip says why nothing is listed: the provider names custom endpoints and none for the type's service.
	skip string
}

func (l *gcpLister) plan(info TypeInfo, req deploy.BindRequest) gcpList {
	if info.TFType == "" {
		return gcpList{fallback: fmt.Sprintf("%s's Terraform type is not known", info.Type)}
	}
	t, reason, ok := gcplist.Lookup(info.TFType)
	if !ok {
		return gcpList{fallback: reason}
	}
	endpoint := providerString(req.ProviderInputs, t.Service.EndpointArg)
	if endpoint == "" && t.Service.EndpointEnv != "" {
		endpoint = os.Getenv(t.Service.EndpointEnv)
	}
	if endpoint == "" {
		if named := gcpCustomEndpoints(req.ProviderInputs); len(named) > 0 {
			return gcpList{t: t, skip: fmt.Sprintf("the google provider sets custom endpoints (%s) and no %s for %s",
				strings.Join(named, ", "), t.Service.EndpointArg, t.Service.Host)}
		}
	}
	u, coll, err := t.URL(t.Root(endpoint), func(name string) (string, bool) { return gcpVar(name, req) })
	if err != nil {
		return gcpList{fallback: fmt.Sprintf("the %s list for %s cannot be addressed: %v", t.Service.Host,
			info.TFType, err)}
	}
	return gcpList{t: t, url: u, coll: coll}
}

// gcpVar fills one variable of a list path: the resource's own input (camelCase, as pulumi-gcp names it), else, for
// the scope variables, the provider's configuration and the provider's environment fallbacks.
func gcpVar(name string, req deploy.BindRequest) (string, bool) {
	scope := name == "project" || name == "region" || name == "zone" || name == "location"
	for _, key := range []string{snakeToCamel(name), name} {
		if v := providerString(req.Inputs, key); v != "" {
			if scope {
				// A self link or a full name ("projects/p", ".../zones/z") names its last segment.
				v = v[strings.LastIndex(v, "/")+1:]
			}
			return v, true
		}
	}
	if !scope {
		return "", false
	}
	a := gcpsweep.Attrs{
		Project: providerString(req.ProviderInputs, "project"),
		Region:  providerString(req.ProviderInputs, "region"),
		Zone:    providerString(req.ProviderInputs, "zone"),
	}.WithEnv(os.Getenv)
	switch name {
	case "project":
		return a.Project, a.Project != ""
	case "zone":
		return a.Zone, a.Zone != ""
	default:
		// pulumi-gcp defaults a regional resource's region, and most location arguments, to the provider's region.
		return a.Region, a.Region != ""
	}
}

func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// ConsistentFor implements requestConsistency.
func (l *gcpLister) ConsistentFor(info TypeInfo, req deploy.BindRequest) (bool, string) {
	p := l.plan(info, req)
	if p.skip != "" {
		return false, "not listed: " + p.skip
	}
	if p.fallback != "" {
		return false, p.fallback + "; Cloud Asset Inventory binds it instead"
	}
	if p.t.Service.Consistency == "" {
		return false, fmt.Sprintf("binds, consistency unmeasured: Google publishes no list consistency model for "+
			"%s, and only Cloud Storage's is claimed", p.t.Service.Host)
	}
	return true, ""
}

// Listing implements listingDescriber.
func (l *gcpLister) Listing(info TypeInfo, req deploy.BindRequest) string {
	p := l.plan(info, req)
	if p.skip != "" {
		return fmt.Sprintf("gcp: not listed (%s)", p.skip)
	}
	if p.fallback != "" {
		return "gcp: Cloud Asset Inventory (eventually consistent)"
	}
	if p.t.Service.Consistency == "" {
		return fmt.Sprintf("gcp: %s list of %s (binds, consistency unmeasured)", p.t.Service.Host, p.coll)
	}
	return fmt.Sprintf("gcp: %s list of %s (strongly consistent); Google documents that %s", p.t.Service.Host,
		p.coll, p.t.Service.Consistency)
}

func (l *gcpLister) ListMarked(
	ctx context.Context, info TypeInfo, req deploy.BindRequest, want map[string]string,
) ([]LiveObject, error) {
	p := l.plan(info, req)
	if p.skip != "" {
		return nil, skipped(req.URN, p.t.Service.Host, "%s", p.skip)
	}
	if p.fallback != "" {
		return l.asset.ListMarked(ctx, info, req, want)
	}
	in := req.ProviderInputs
	a := gcpsweep.Attrs{
		Project:                   providerString(in, "project"),
		Credentials:               providerString(in, "credentials"),
		AccessToken:               providerString(in, "accessToken"),
		ImpersonateServiceAccount: providerString(in, "impersonateServiceAccount"),
		IAMCredentialsEndpoint:    providerString(in, "iamCredentialsCustomEndpoint"),
	}.WithEnv(os.Getenv)
	if err := gcpImpersonationEndpoint(req, a); err != nil {
		return nil, err
	}
	// A stack naming custom endpoints gets a client whose credential never fetches a token from a non-local host
	// (INTENTIUS/plunge#42, gcpauth.go); any other keeps the provider's own credential chain.
	local, isLocal, err := gcpLocalAuthFor(req, a, p.t.Service.Host)
	if err != nil {
		return nil, err
	}
	var c *http.Client
	if isLocal {
		c, err = l.clients.get(local.cacheKey(), func() (*http.Client, error) { return local.client(ctx) })
	} else {
		key := a.Credentials + "|" + a.AccessToken + "|" + a.ImpersonateServiceAccount
		c, err = l.clients.get(key, func() (*http.Client, error) { return gcpsweep.HTTPClient(ctx, a) })
	}
	if err != nil {
		return nil, err
	}
	objs, err := p.t.List(ctx, c, p.url, p.coll)
	if err != nil {
		return nil, err
	}
	out := make([]LiveObject, 0, len(objs))
	for _, o := range objs {
		out = append(out, LiveObject{ID: resource.ID(p.t.ImportID(o)), Tags: o.Labels})
	}
	return filterMarked(out, want), nil
}

// assetLister binds Google Cloud resources through Cloud Asset Inventory (gcpsweep). The index is eventually
// consistent, so a bind it makes is sound but its silence is not proof there is no duplicate. It is gcpLister's
// fallback for the types gcplist does not cover, and the collision claim does not cover those.
type assetLister struct {
	clients listerCache[gcpsweep.Client]
}

func (l *assetLister) Consistent() bool { return false }

func (l *assetLister) Covers(info TypeInfo) (bool, string) { return true, "" }

func (l *assetLister) ListMarked(
	ctx context.Context, info TypeInfo, req deploy.BindRequest, want map[string]string,
) ([]LiveObject, error) {
	in := req.ProviderInputs
	a := gcpsweep.Attrs{
		Project:                   providerString(in, "project"),
		Credentials:               providerString(in, "credentials"),
		AccessToken:               providerString(in, "accessToken"),
		ImpersonateServiceAccount: providerString(in, "impersonateServiceAccount"),
		CloudAssetEndpoint:        providerString(in, "cloudAssetCustomEndpoint"),
		IAMCredentialsEndpoint:    providerString(in, "iamCredentialsCustomEndpoint"),
	}.WithEnv(os.Getenv)
	if a.CloudAssetEndpoint == "" {
		if named := gcpCustomEndpoints(in); len(named) > 0 {
			return nil, skipped(req.URN, "cloudasset.googleapis.com", "the google provider sets custom endpoints "+
				"(%s) and no cloudAssetCustomEndpoint", strings.Join(named, ", "))
		}
	}
	if err := gcpImpersonationEndpoint(req, a); err != nil {
		return nil, err
	}
	if a.Project == "" {
		return nil, fmt.Errorf("no project: the provider sets no project and GOOGLE_PROJECT is unset")
	}
	local, isLocal, err := gcpLocalAuthFor(req, a, "cloudasset.googleapis.com")
	if err != nil {
		return nil, err
	}
	var c *gcpsweep.Client
	if isLocal {
		// INTENTIUS/plunge#42: the Cloud Asset client goes over plunge's own client, never the copy's credential
		// lookup (gcpauth.go).
		c, err = l.clients.get(a.Project+"|"+local.cacheKey(), func() (*gcpsweep.Client, error) {
			hc, err := local.client(ctx)
			if err != nil {
				return nil, err
			}
			return gcpsweep.NewWithHTTPClient(ctx, a, hc, gcpsweep.Credentials{
				Source: gcpsweep.SourceCaller, Impersonate: local.impersonate,
			})
		})
	} else {
		c, err = l.clients.get(a.Project+"|"+a.ImpersonateServiceAccount, func() (*gcpsweep.Client, error) {
			return gcpsweep.New(ctx, a)
		})
	}
	if err != nil {
		return nil, err
	}
	assets, err := c.SearchAddress(ctx, a.Project, want[markers.TagStack], want[markers.TagURN])
	if err != nil {
		return nil, err
	}
	var out []LiveObject
	for _, as := range assets {
		// "//compute.googleapis.com/projects/p/global/networks/n" is the provider's "projects/p/global/networks/n".
		id := strings.TrimPrefix(as.Name, "//")
		if i := strings.Index(id, "/"); i >= 0 {
			id = id[i+1:]
		}
		out = append(out, LiveObject{ID: resource.ID(id), Tags: as.Labels})
	}
	return filterMarked(out, want), nil
}

// gcpImpersonationEndpoint refuses an impersonated token request that would go to the public IAM Credentials API
// although the provider names custom endpoints for other services.
func gcpImpersonationEndpoint(req deploy.BindRequest, a gcpsweep.Attrs) error {
	if a.ImpersonateServiceAccount == "" || a.IAMCredentialsEndpoint != "" {
		return nil
	}
	if named := gcpCustomEndpoints(req.ProviderInputs); len(named) > 0 {
		return skipped(req.URN, "iamcredentials.googleapis.com", "the google provider impersonates %s and sets "+
			"custom endpoints (%s) but no iamCredentialsCustomEndpoint", a.ImpersonateServiceAccount,
			strings.Join(named, ", "))
	}
	return nil
}
