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
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/gcpsweep"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/kubesweep"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/registry"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// EnvCloudControlEndpoint points the Cloud Control lister at an emulator ("http://localhost:4566").
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
		// pulumi-aws's ID is the Terraform id. It is the Cloud Control identifier only for a single-part one;
		// a multi-part identifier needs a per-type translation plunge does not have yet.
		if roster, err := registry.Embedded(); err != nil || roster.IdentifierArity(info.CFNType) != 1 {
			return false, fmt.Sprintf("%s's Cloud Control identifier is not a single part, so it cannot stand for "+
				"the Terraform id of %s", info.CFNType, info.TFType)
		}
	}
	return true, ""
}

func (l *cloudControlLister) ListMarked(
	ctx context.Context, info TypeInfo, req deploy.BindRequest, want map[string]string,
) ([]LiveObject, error) {
	region := providerString(req.ProviderInputs, "region")
	if region == "" {
		region = firstEnv("AWS_REGION", "AWS_DEFAULT_REGION")
	}
	endpoint := os.Getenv(EnvCloudControlEndpoint)
	c, err := l.clients.get(region+"|"+endpoint, func() (*cloudcontrol.Client, error) {
		return cloudcontrol.New(cloudcontrol.Config{Region: region, Endpoint: endpoint}), nil
	})
	if err != nil {
		return nil, err
	}
	descs, err := c.ListResources(ctx, info.CFNType)
	if err != nil {
		return nil, err
	}
	var objs []LiveObject
	for _, d := range descs {
		tags, ok := cfnTags(d.Properties)
		if !ok {
			// The list handler left the tags out: read the one resource.
			full, err := cloudcontrol.GetResourceByIdentity(ctx, c, info.CFNType, d.Identifier)
			if err != nil {
				return nil, err
			}
			if full == nil {
				continue
			}
			tags, _ = cfnTags(full.Properties)
		}
		objs = append(objs, LiveObject{ID: resource.ID(d.Identifier), Tags: tags})
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

func (l *armLister) Consistent() bool { return true }

func (l *armLister) Covers(info TypeInfo) (bool, string) { return true, "" }

func (l *armLister) ListMarked(
	ctx context.Context, info TypeInfo, req deploy.BindRequest, want map[string]string,
) ([]LiveObject, error) {
	in := req.ProviderInputs
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

// assetLister binds Google Cloud resources through Cloud Asset Inventory (gcpsweep). The index is eventually
// consistent, so a bind it makes is sound but its silence is not proof there is no duplicate; Google Cloud has no
// per-type tagged list of its own across services. The collision claim does not cover Google Cloud types.
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
	}.WithEnv(os.Getenv)
	if a.Project == "" {
		return nil, fmt.Errorf("no project: the provider sets no project and GOOGLE_PROJECT is unset")
	}
	c, err := l.clients.get(a.Project+"|"+a.ImpersonateServiceAccount, func() (*gcpsweep.Client, error) {
		return gcpsweep.New(ctx, a)
	})
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
