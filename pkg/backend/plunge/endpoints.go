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

// Where the family listers send their calls when the stack points its provider at custom endpoints
// (INTENTIUS/plunge#27). A stack whose provider configuration sends every call to an emulator says "never the real
// cloud", and the listers keep to that: each takes its endpoint from the same provider configuration (and the
// environment variables the provider reads), and when that configuration names custom endpoints but none for the
// service a listing would call, the listing is skipped with a warning naming the stack. A lister never falls back
// to the cloud's public endpoint in that case.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// listerTransport is what the Cloud Control lister's requests are sent over; nil is http.DefaultTransport. Tests set
// it to a transport that fails on any host that is not the loopback address.
var listerTransport http.RoundTripper

// SkippedListing is a listing plunge did not make: the stack's provider configuration names custom endpoints and
// none for the service the listing calls, so making it would have meant calling the cloud's public endpoint.
type SkippedListing struct {
	// Stack is the stack whose configuration it is.
	Stack string
	// Service is what the listing would have called ("AWS Cloud Control", "ARM", "compute.googleapis.com").
	Service string
	// Reason says what the configuration names.
	Reason string
}

func (e *SkippedListing) Error() string {
	return fmt.Sprintf("plunge did not list through %s for stack %s: %s, and plunge never falls back to the public "+
		"endpoint when the stack's provider configuration points at custom endpoints", e.Service, e.Stack, e.Reason)
}

// skipped is the SkippedListing for a listing made for urn's stack.
func skipped(urn resource.URN, service, format string, args ...any) *SkippedListing {
	stack := "(unknown)"
	if urn.IsValid() {
		stack = string(urn.Stack())
	}
	return &SkippedListing{Stack: stack, Service: service, Reason: fmt.Sprintf(format, args...)}
}

// awsEndpoints reads pulumi-aws's endpoints provider input, a list of objects mapping a service to its URL, as
// service -> URL with the service lowercased. A default provider carries it as the JSON text of the stack's
// aws:endpoints config value; an explicit provider as the list itself.
func awsEndpoints(inputs resource.PropertyMap) map[string]string {
	v, ok := inputs["endpoints"]
	if !ok {
		return nil
	}
	v = unwrapValue(v)
	out := map[string]string{}
	addMap := func(m map[string]any) {
		for k, x := range m {
			if s, ok := x.(string); ok && s != "" {
				out[strings.ToLower(k)] = s
			}
		}
	}
	addObj := func(o resource.PropertyMap) {
		for k, x := range o {
			if x = unwrapValue(x); x.IsString() && x.StringValue() != "" {
				out[strings.ToLower(string(k))] = x.StringValue()
			}
		}
	}
	switch {
	case v.IsString():
		var list []map[string]any
		if err := json.Unmarshal([]byte(v.StringValue()), &list); err == nil {
			for _, m := range list {
				addMap(m)
			}
			break
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(v.StringValue()), &m); err == nil {
			addMap(m)
		}
	case v.IsArray():
		for _, e := range v.ArrayValue() {
			if e = unwrapValue(e); e.IsObject() {
				addObj(e.ObjectValue())
			}
		}
	case v.IsObject():
		addObj(v.ObjectValue())
	}
	return out
}

// envNamesWith lists the set environment variables whose names start with prefix and end with suffix.
func envNamesWith(prefix, suffix string) []string {
	var out []string
	for _, kv := range os.Environ() {
		name, val, _ := strings.Cut(kv, "=")
		if val != "" && strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// cloudControlEndpoint is where the Cloud Control lister calls for a provider with these inputs: "" for AWS's public
// endpoint. In order: PLUNGE_CLOUDCONTROL_ENDPOINT; the provider's endpoints entry for Cloud Control; the
// environment variables the provider reads for it (AWS_ENDPOINT_URL_CLOUDCONTROL, then AWS_ENDPOINT_URL); the one
// custom endpoint every service in the provider's endpoints shares. A non-empty skip says why there is no endpoint
// to call although the configuration names custom ones.
func cloudControlEndpoint(inputs resource.PropertyMap) (endpoint, skip string) {
	if v := os.Getenv(EnvCloudControlEndpoint); v != "" {
		return v, ""
	}
	eps := awsEndpoints(inputs)
	for _, k := range []string{"cloudcontrol", "cloudcontrolapi"} {
		if v := eps[k]; v != "" {
			return v, ""
		}
	}
	for _, n := range []string{"AWS_ENDPOINT_URL_CLOUDCONTROL", "AWS_ENDPOINT_URL"} {
		if v := os.Getenv(n); v != "" {
			return v, ""
		}
	}
	shared := ""
	for _, v := range eps {
		if shared == "" {
			shared = v
		} else if v != shared {
			shared = ""
			break
		}
	}
	if shared != "" {
		return shared, ""
	}
	if len(eps) > 0 {
		return "", fmt.Sprintf("the aws provider's endpoints name %s at more than one URL and no cloudcontrol entry "+
			"(set one in aws:endpoints, or %s)", strings.Join(sortedKeys(eps), ", "), EnvCloudControlEndpoint)
	}
	if names := envNamesWith("AWS_ENDPOINT_URL_", ""); len(names) > 0 {
		return "", fmt.Sprintf("the environment sets %s and not AWS_ENDPOINT_URL_CLOUDCONTROL (set it, or %s)",
			strings.Join(names, ", "), EnvCloudControlEndpoint)
	}
	return "", ""
}

// gcpCustomEndpoints names the custom endpoints the google provider's configuration sets (pulumi-gcp's
// "<service>CustomEndpoint" inputs) and the GOOGLE_*_CUSTOM_ENDPOINT variables the provider reads.
func gcpCustomEndpoints(inputs resource.PropertyMap) []string {
	var out []string
	for k := range inputs {
		if strings.HasSuffix(string(k), "CustomEndpoint") && providerString(inputs, string(k)) != "" {
			out = append(out, string(k))
		}
	}
	sort.Strings(out)
	return append(out, envNamesWith("GOOGLE_", "_CUSTOM_ENDPOINT")...)
}

// azureMetadataHost is the metadata host the azure provider's configuration (metadataHost) or its environment
// (ARM_METADATA_HOSTNAME) names: the host an emulator such as floci-az serves the cloud's endpoints from.
func azureMetadataHost(inputs resource.PropertyMap) string {
	if v := providerString(inputs, "metadataHost"); v != "" {
		return v
	}
	return os.Getenv("ARM_METADATA_HOSTNAME")
}
