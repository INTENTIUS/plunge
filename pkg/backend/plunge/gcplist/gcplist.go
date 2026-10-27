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

// Package gcplist lists one Google Cloud type's live objects through the type's own service API, the per-service
// lister INTENTIUS/plunge#15 was ruled to get (2026-10-25: every covered GCP type; choudoufu gets the same in
// INTENTIUS/choudoufu#2264). Cloud Asset Inventory, which gcpsweep searches, is an index that lags its services by
// minutes; a service's own list call reads the store the create wrote to, which is what the collision claim (C2)
// needs, as Cloud Control's ListResources is for AWS.
//
// The package is keyed by Terraform type ("google_pubsub_topic"), so the same table serves pulumi-gcp (whose
// mapping names the Terraform type) and hashicorp/google.
//
// # Which types are covered
//
// A type is covered when both hold:
//
//   - choudoufu's pkg/identity knows its Asset name ([identity.GoogleAssetImports], generated from the provider
//     source by tools/gcp-row-gen): the template says where the object lives ("projects/{{project}}/topics/{{name}}")
//     and how its name becomes an import ID.
//   - Its service is in [Services]: plunge knows the service's REST root, the provider argument that overrides it, and
//     the field its list response carries the objects in.
//
// The list call is the template's parent collection: the Asset template with its last segment (the object's own
// name) dropped, "projects/{{project}}/topics" for a topic. That is AIP-132's standard List, which every resource
// oriented service in [Services] serves, and the older Compute Engine and Cloud Storage JSON APIs serve the same
// collection paths. A type outside the covered set is listed through Asset Inventory as before: it binds, and the
// collision claim does not cover it.
//
// Ten server-assigned types whose services are not in [Services] (Vertex AI, Document AI, Transcoder, Service
// Directory, reCAPTCHA Enterprise, Managed Microsoft AD) are written out in [ServerAssigned], ported from choudoufu's
// gcpsweep. Three types with no Asset name (Cloud SQL instances, GKE clusters, Assured Workloads workloads) are
// written out in [Handwritten] (INTENTIUS/plunge#19). The covered set is the same in both repos:
// testdata/covered-types.txt is a copy of choudoufu's pkg/gcpsweep/testdata/covered-types.txt, and each
// repo tests its own set against its copy.
//
// # Consistency
//
// Each list reads the service's own store. Cloud Storage documents bucket listing as strongly consistent
// (https://cloud.google.com/storage/docs/consistency), and it is the one service whose list carries the collision
// claim C2. The other services publish no list consistency model, so their lists bind and their consistency is
// unmeasured (ruled on INTENTIUS/plunge#15, 2026-10-26). A [Service] with a Consistency statement is a claimed one.
package gcplist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/pkg/identity"
)

// Service is one Google Cloud service plunge lists through.
type Service struct {
	// Host is the service host Asset names carry: "pubsub.googleapis.com".
	Host string
	// Root is the service's REST root with its version, as the provider's default endpoint spells it:
	// "https://pubsub.googleapis.com/v1/".
	Root string
	// EndpointArg is pulumi-gcp's custom endpoint argument for the service ("pubsubCustomEndpoint"). Its value
	// replaces Root, version included, the way the provider reads it; an emulator such as floci-gcp sets it.
	EndpointArg string
	// EndpointEnv is the environment variable the provider reads for EndpointArg when it is unset.
	EndpointEnv string
	// Items is the list response's field holding the objects. Empty is AIP-132's: the collection's own name
	// ("topics"). Compute Engine and Cloud Storage use "items".
	Items string
	// Consistency is Google's statement of the list's consistency, when it publishes one.
	Consistency string
}

// Services are the services plunge lists through, by host.
var Services = map[string]Service{}

func init() {
	for _, s := range []Service{
		{Host: "compute.googleapis.com", Root: "https://compute.googleapis.com/compute/v1/",
			EndpointArg: "computeCustomEndpoint", EndpointEnv: "GOOGLE_COMPUTE_CUSTOM_ENDPOINT", Items: "items"},
		{Host: "storage.googleapis.com", Root: "https://storage.googleapis.com/storage/v1/",
			EndpointArg: "storageCustomEndpoint", EndpointEnv: "GOOGLE_STORAGE_CUSTOM_ENDPOINT", Items: "items",
			Consistency: "bucket listing is strongly consistent (https://cloud.google.com/storage/docs/consistency)"},
		{Host: "pubsub.googleapis.com", Root: "https://pubsub.googleapis.com/v1/",
			EndpointArg: "pubsubCustomEndpoint", EndpointEnv: "GOOGLE_PUBSUB_CUSTOM_ENDPOINT"},
		{Host: "run.googleapis.com", Root: "https://run.googleapis.com/v2/",
			EndpointArg: "cloudRunV2CustomEndpoint", EndpointEnv: "GOOGLE_CLOUD_RUN_V2_CUSTOM_ENDPOINT"},
		{Host: "cloudfunctions.googleapis.com", Root: "https://cloudfunctions.googleapis.com/v2/",
			EndpointArg: "cloudfunctions2CustomEndpoint", EndpointEnv: "GOOGLE_CLOUDFUNCTIONS2_CUSTOM_ENDPOINT"},
		{Host: "secretmanager.googleapis.com", Root: "https://secretmanager.googleapis.com/v1/",
			EndpointArg: "secretManagerCustomEndpoint", EndpointEnv: "GOOGLE_SECRET_MANAGER_CUSTOM_ENDPOINT"},
		{Host: "artifactregistry.googleapis.com", Root: "https://artifactregistry.googleapis.com/v1/",
			EndpointArg: "artifactRegistryCustomEndpoint", EndpointEnv: "GOOGLE_ARTIFACT_REGISTRY_CUSTOM_ENDPOINT"},
		{Host: "cloudkms.googleapis.com", Root: "https://cloudkms.googleapis.com/v1/",
			EndpointArg: "kmsCustomEndpoint", EndpointEnv: "GOOGLE_KMS_CUSTOM_ENDPOINT"},
		{Host: "dns.googleapis.com", Root: "https://dns.googleapis.com/dns/v1/",
			EndpointArg: "dnsCustomEndpoint", EndpointEnv: "GOOGLE_DNS_CUSTOM_ENDPOINT"},
		{Host: "redis.googleapis.com", Root: "https://redis.googleapis.com/v1/",
			EndpointArg: "redisCustomEndpoint", EndpointEnv: "GOOGLE_REDIS_CUSTOM_ENDPOINT"},
		{Host: "memcache.googleapis.com", Root: "https://memcache.googleapis.com/v1/",
			EndpointArg: "memcacheCustomEndpoint", EndpointEnv: "GOOGLE_MEMCACHE_CUSTOM_ENDPOINT"},
		{Host: "file.googleapis.com", Root: "https://file.googleapis.com/v1/",
			EndpointArg: "filestoreCustomEndpoint", EndpointEnv: "GOOGLE_FILESTORE_CUSTOM_ENDPOINT"},
		{Host: "spanner.googleapis.com", Root: "https://spanner.googleapis.com/v1/",
			EndpointArg: "spannerCustomEndpoint", EndpointEnv: "GOOGLE_SPANNER_CUSTOM_ENDPOINT"},
		{Host: "alloydb.googleapis.com", Root: "https://alloydb.googleapis.com/v1/",
			EndpointArg: "alloydbCustomEndpoint", EndpointEnv: "GOOGLE_ALLOYDB_CUSTOM_ENDPOINT"},
		{Host: "apigateway.googleapis.com", Root: "https://apigateway.googleapis.com/v1/",
			EndpointArg: "apiGatewayCustomEndpoint", EndpointEnv: "GOOGLE_API_GATEWAY_CUSTOM_ENDPOINT"},
		{Host: "eventarc.googleapis.com", Root: "https://eventarc.googleapis.com/v1/",
			EndpointArg: "eventarcCustomEndpoint", EndpointEnv: "GOOGLE_EVENTARC_CUSTOM_ENDPOINT"},
		{Host: "certificatemanager.googleapis.com", Root: "https://certificatemanager.googleapis.com/v1/",
			EndpointArg: "certificateManagerCustomEndpoint", EndpointEnv: "GOOGLE_CERTIFICATE_MANAGER_CUSTOM_ENDPOINT"},
		{Host: "privateca.googleapis.com", Root: "https://privateca.googleapis.com/v1/",
			EndpointArg: "privatecaCustomEndpoint", EndpointEnv: "GOOGLE_PRIVATECA_CUSTOM_ENDPOINT"},
		{Host: "clouddeploy.googleapis.com", Root: "https://clouddeploy.googleapis.com/v1/",
			EndpointArg: "clouddeployCustomEndpoint", EndpointEnv: "GOOGLE_CLOUDDEPLOY_CUSTOM_ENDPOINT"},
		{Host: "networkservices.googleapis.com", Root: "https://networkservices.googleapis.com/v1/",
			EndpointArg: "networkServicesCustomEndpoint", EndpointEnv: "GOOGLE_NETWORK_SERVICES_CUSTOM_ENDPOINT"},
		{Host: "networksecurity.googleapis.com", Root: "https://networksecurity.googleapis.com/v1/",
			EndpointArg: "networkSecurityCustomEndpoint", EndpointEnv: "GOOGLE_NETWORK_SECURITY_CUSTOM_ENDPOINT"},
		{Host: "networkconnectivity.googleapis.com", Root: "https://networkconnectivity.googleapis.com/v1/",
			EndpointArg: "networkConnectivityCustomEndpoint", EndpointEnv: "GOOGLE_NETWORK_CONNECTIVITY_CUSTOM_ENDPOINT"},
		{Host: "dataproc.googleapis.com", Root: "https://dataproc.googleapis.com/v1/",
			EndpointArg: "dataprocCustomEndpoint", EndpointEnv: "GOOGLE_DATAPROC_CUSTOM_ENDPOINT"},
		{Host: "workstations.googleapis.com", Root: "https://workstations.googleapis.com/v1/",
			EndpointArg: "workstationsCustomEndpoint", EndpointEnv: "GOOGLE_WORKSTATIONS_CUSTOM_ENDPOINT"},
		{Host: "datastream.googleapis.com", Root: "https://datastream.googleapis.com/v1/",
			EndpointArg: "datastreamCustomEndpoint", EndpointEnv: "GOOGLE_DATASTREAM_CUSTOM_ENDPOINT"},
		{Host: "metastore.googleapis.com", Root: "https://metastore.googleapis.com/v1/",
			EndpointArg: "dataprocMetastoreCustomEndpoint", EndpointEnv: "GOOGLE_DATAPROC_METASTORE_CUSTOM_ENDPOINT"},
		{Host: "managedkafka.googleapis.com", Root: "https://managedkafka.googleapis.com/v1/",
			EndpointArg: "managedKafkaCustomEndpoint", EndpointEnv: "GOOGLE_MANAGED_KAFKA_CUSTOM_ENDPOINT"},
		{Host: "parametermanager.googleapis.com", Root: "https://parametermanager.googleapis.com/v1/",
			EndpointArg: "parameterManagerCustomEndpoint", EndpointEnv: "GOOGLE_PARAMETER_MANAGER_CUSTOM_ENDPOINT"},
	} {
		Services[s.Host] = s
	}
}

// Type is one covered type: where its objects are listed.
type Type struct {
	TFType  string
	Service Service
	// Collection is the list path under the service root, in ReplaceVars syntax: "projects/{{project}}/topics".
	Collection string
	// Query is extra query parameters, in the same syntax: Cloud Storage lists a project's buckets as
	// "b?project={{project}}".
	Query map[string]string
	// Asset is the Asset name template the type's objects are named by (identity.GoogleAssetImports).
	Asset string
	// LabelsAt is where a listed object keeps its labels, as a path of fields; empty is "labels". Cloud SQL keeps
	// them at settings.userLabels and GKE at resourceLabels.
	LabelsAt []string
	// ItemName names a listed object, in place of the rule objectName applies; nil for that rule.
	ItemName func(o map[string]any, coll string) string
}

// Lookup returns tfType's list, or a reason it has none. A server-assigned type ([ServerAssigned]) is written out;
// every other covered type is derived from its Asset name and its service.
func Lookup(tfType string) (Type, string, bool) {
	if t, ok := serverAssigned(tfType); ok {
		t.Asset = identity.GoogleAssetImports[tfType].Asset
		return t, "", true
	}
	if t, ok := handwritten(tfType); ok {
		return t, "", true
	}
	if why, ok := NotCovered[tfType]; ok {
		return Type{}, tfType + " has no per-type lister: " + why, false
	}
	row, ok := identity.GoogleAssetImports[tfType]
	if !ok {
		return Type{}, fmt.Sprintf("choudoufu's pkg/identity has no Asset name for %s, so there is no collection to "+
			"list it from", tfType), false
	}
	svc, ok := Services[row.Host]
	if !ok {
		return Type{}, fmt.Sprintf("plunge has no per-service lister for %s (%s)", row.Host, tfType), false
	}
	t := Type{TFType: tfType, Service: svc, Asset: row.Asset}
	if row.Host == "storage.googleapis.com" {
		// A bucket's Asset name is its bare name; the JSON API lists a project's buckets at b?project=.
		if row.Asset != "{{bucket}}" && row.Asset != "{{name}}" {
			return Type{}, fmt.Sprintf("%s's Asset name %q is not a bucket's", tfType, row.Asset), false
		}
		t.Collection, t.Query = "b", map[string]string{"project": "{{project}}"}
		return t, "", true
	}
	segs := strings.Split(row.Asset, "/")
	if len(segs) < 2 || !isPlaceholder(segs[len(segs)-1]) || strings.Contains(segs[len(segs)-2], "{{") {
		return Type{}, fmt.Sprintf("%s's Asset name %q does not end in a collection and a name", tfType, row.Asset),
			false
	}
	t.Collection = strings.Join(segs[:len(segs)-1], "/")
	return t, "", true
}

// Covered lists every covered type, sorted: the server-assigned ones and the derived ones.
func Covered() []string {
	seen := map[string]bool{}
	var out []string
	for tf := range ServerAssigned {
		seen[tf] = true
		out = append(out, tf)
	}
	for tf := range Handwritten {
		seen[tf] = true
		out = append(out, tf)
	}
	for tf := range identity.GoogleAssetImports {
		if _, _, ok := Lookup(tf); ok && !seen[tf] {
			out = append(out, tf)
		}
	}
	sort.Strings(out)
	return out
}

var placeholderRe = regexp.MustCompile(`\{\{%?([a-zA-Z0-9_]+)\}\}`)

func isPlaceholder(seg string) bool {
	m := placeholderRe.FindStringSubmatchIndex(seg)
	return m != nil && m[0] == 0 && m[1] == len(seg)
}

// Placeholders lists the variables t's list needs, in order, each once.
func (t Type) Placeholders() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	add(t.Collection)
	keys := make([]string, 0, len(t.Query))
	for k := range t.Query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		add(t.Query[k])
	}
	return out
}

// Root is the service root a list is made against: endpoint (the provider's custom endpoint) when set, else the
// service's default. Either way it ends in "/".
func (t Type) Root(endpoint string) string {
	if endpoint == "" {
		endpoint = t.Service.Root
	}
	return strings.TrimRight(endpoint, "/") + "/"
}

// ErrUnresolved is returned when a variable the list needs has no value.
var ErrUnresolved = errors.New("unresolved")

func fill(tmpl string, vars func(string) (string, bool)) (string, error) {
	var missing []string
	out := placeholderRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		name := placeholderRe.FindStringSubmatch(m)[1]
		v, ok := vars(name)
		if !ok || v == "" {
			missing = append(missing, name)
			return m
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%w: %s", ErrUnresolved, strings.Join(missing, ", "))
	}
	return out, nil
}

// URL is t's first-page list URL under root, its variables filled by vars. An unset variable is an error wrapping
// ErrUnresolved naming it. A root may carry {{region}} or {{location}} (a regional host); it is filled too.
func (t Type) URL(root string, vars func(string) (string, bool)) (string, string, error) {
	root, err := fill(root, vars)
	if err != nil {
		return "", "", err
	}
	coll, err := fill(t.Collection, vars)
	if err != nil {
		return "", "", err
	}
	q := url.Values{}
	for k, v := range t.Query {
		fv, err := fill(v, vars)
		if err != nil {
			return "", "", err
		}
		q.Set(k, fv)
	}
	u := root + coll
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u, coll, nil
}

// Object is one listed object.
type Object struct {
	// Name is the object's Asset name without the "//host/" prefix: "projects/p/topics/t", a bucket's bare name.
	Name string
	// AssetName is the full Asset name: "//pubsub.googleapis.com/projects/p/topics/t".
	AssetName string
	Labels    map[string]string
}

// ImportID is o's import ID for t: the Asset name rewritten by pkg/identity, else the name as listed.
func (t Type) ImportID(o Object) string {
	if id, ok := identity.GoogleAssetImportID(t.TFType, o.AssetName); ok {
		return id
	}
	return o.Name
}

// List reads every page of t's list at first and returns its objects. coll is the filled collection path (URL's
// second result), which names an object listed by its bare name.
func (t Type) List(ctx context.Context, c *http.Client, first, coll string) ([]Object, error) {
	items := t.Service.Items
	if items == "" {
		items = coll[strings.LastIndex(coll, "/")+1:]
	}
	var out []Object
	next := ""
	for page := 0; ; page++ {
		u := first
		if next != "" {
			sep := "?"
			if strings.Contains(u, "?") {
				sep = "&"
			}
			u += sep + "pageToken=" + url.QueryEscape(next)
		}
		body, err := get(ctx, c, u)
		if err != nil {
			return nil, err
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("%s: the list response is not a JSON object: %w", t.Service.Host, err)
		}
		var objs []map[string]any
		if v, ok := raw[items]; ok {
			if err := json.Unmarshal(v, &objs); err != nil {
				return nil, fmt.Errorf("%s: the list response's %q is not a list of objects: %w", t.Service.Host,
					items, err)
			}
		}
		for _, o := range objs {
			name := t.objectName(o, coll)
			if name == "" {
				continue
			}
			out = append(out, Object{Name: name, AssetName: "//" + t.Service.Host + "/" + name,
				Labels: labels(o, t.LabelsAt)})
		}
		next = ""
		if v, ok := raw["nextPageToken"]; ok {
			_ = json.Unmarshal(v, &next)
		}
		if next == "" {
			return out, nil
		}
		if page > 10000 {
			return nil, fmt.Errorf("%s: the list did not end after 10000 pages", t.Service.Host)
		}
	}
}

// objectName is an object's Asset path. An AIP resource's name is its full relative name already. A Compute Engine
// object's name is bare, and its selfLink carries the path. Anything else is named under the collection, except a
// bucket, whose Asset name is its bare name.
func (t Type) objectName(o map[string]any, coll string) string {
	if t.ItemName != nil {
		return t.ItemName(o, coll)
	}
	name, _ := o["name"].(string)
	if strings.Contains(name, "/") {
		return strings.TrimPrefix(name, "//"+t.Service.Host+"/")
	}
	if self, ok := o["selfLink"].(string); ok {
		if i := strings.Index(self, "/projects/"); i >= 0 {
			return self[i+1:]
		}
	}
	if name == "" {
		return ""
	}
	if t.Service.Host == "storage.googleapis.com" {
		return name
	}
	return coll + "/" + name
}

func labels(o map[string]any, at []string) map[string]string {
	out := map[string]string{}
	if len(at) == 0 {
		at = []string{"labels"}
	}
	var cur any = o
	for _, f := range at {
		obj, _ := cur.(map[string]any)
		cur = obj[f]
	}
	m, _ := cur.(map[string]any)
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func get(ctx context.Context, c *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s answered HTTP %d: %s", u, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}
