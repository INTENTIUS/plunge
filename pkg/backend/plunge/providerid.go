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
	"encoding/json"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// providerIDNamespace is the UUID namespace plunge derives provider IDs in. Fixed forever: changing it changes
// every new provider's ID.
var providerIDNamespace = uuid.Must(uuid.FromString("6c1f3c9e-2a0e-5b7a-9d43-70c1b3a8e5d2"))

// DeterministicProviderID derives a provider resource's ID from its URN and its filtered configuration, as a
// version 5 UUID, in place of the random UUID stock Pulumi assigns (pkg/resource/deploy/providers/registry.go,
// Registry.Create). Two runs creating the same default provider with the same configuration get the same ID, so
// their resources' URN::ID provider references agree and the records converge (INTENTIUS/plunge#6).
//
// Secret configuration values are hashed as a fixed placeholder, never their plaintext, so a provider ID says
// nothing about a secret. Two configurations differing only in a secret value therefore share an ID; they still
// conflict on the provider's record if they differ, which is the named-conflict path.
//
// The known limit: a replacement whose new configuration is identical to the old one (a forced replace) derives
// the old ID again. That case is not handled here; it is listed on INTENTIUS/plunge#6.
func DeterministicProviderID(urn resource.URN, inputs resource.PropertyMap) (resource.ID, error) {
	canon, err := json.Marshal(hashable(resource.NewObjectProperty(inputs)))
	if err != nil {
		return "", fmt.Errorf("encoding the configuration of %s: %w", urn, err)
	}
	name := append([]byte(urn+"\x00"), canon...)
	return resource.ID(uuid.NewV5(providerIDNamespace, string(name)).String()), nil
}

// hashable maps a property value to plain JSON with secrets and unknowns replaced by fixed markers.
func hashable(v resource.PropertyValue) any {
	switch {
	case v.IsSecret():
		return map[string]any{"<secret>": true}
	case v.IsComputed(), v.IsOutput() && !v.OutputValue().Known:
		return map[string]any{"<unknown>": true}
	case v.IsOutput():
		return hashable(v.OutputValue().Element)
	case v.IsObject():
		out := map[string]any{}
		for k, e := range v.ObjectValue() {
			out[string(k)] = hashable(e)
		}
		return out
	case v.IsArray():
		arr := v.ArrayValue()
		out := make([]any, len(arr))
		for i, e := range arr {
			out[i] = hashable(e)
		}
		return out
	case v.IsResourceReference():
		ref := v.ResourceReferenceValue()
		return map[string]any{"<ref>": string(ref.URN)}
	case v.IsAsset():
		return map[string]any{"<asset>": v.AssetValue().Hash}
	case v.IsArchive():
		return map[string]any{"<archive>": v.ArchiveValue().Hash}
	default:
		return v.V
	}
}
