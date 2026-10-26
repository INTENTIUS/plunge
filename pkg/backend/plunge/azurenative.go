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

// azure-native marker placement from the provider's own schema (INTENTIUS/plunge#15). azure-native generates every
// resource from ARM's specifications, and a resource that ARM tags has a "tags" input that is a map of strings. The
// schema is the whole ARM surface, tens of megabytes, so it is read once per provider version and distilled to the
// one fact plunge needs per type, its tags shape, which is cached on disk (PLUNGE_AZURE_NATIVE_CACHE names the
// directory, "off" turns the cache off; the default is the user cache directory's plunge/ folder).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
)

// EnvAzureNativeCache names the directory azure-native's distilled tags table is cached in, or "off".
const EnvAzureNativeCache = "PLUNGE_AZURE_NATIVE_CACHE"

// azureNativeTags is one azure-native version's tags shape per resource token.
type azureNativeTags struct {
	Version string `json:"version"`
	// Types maps a resource token to its tags shape: ShapeMap, ShapeNone or ShapeUnsupported. CreateOnly lists the
	// tokens whose tags replace the resource when changed.
	Types      map[string]TagShape `json:"types"`
	CreateOnly map[string]bool     `json:"createOnly,omitempty"`
}

type azureNativeLoader struct {
	mu       sync.Mutex
	versions map[string]*azureNativeTags
	failed   map[string]error
	// cacheDir is where distilled tables are kept; "" keeps them in memory only.
	cacheDir string
}

func newAzureNativeLoader() *azureNativeLoader {
	dir := os.Getenv(EnvAzureNativeCache)
	switch dir {
	case "off":
		dir = ""
	case "":
		if base, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(base, "plunge")
		}
	}
	return &azureNativeLoader{versions: map[string]*azureNativeTags{}, failed: map[string]error{}, cacheDir: dir}
}

// load returns version's table, reading prov's schema at most once per version per process and once per cache.
func (l *azureNativeLoader) load(ctx context.Context, prov plugin.Provider, version string) (*azureNativeTags, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.versions[version]; ok {
		return t, nil
	}
	if err, ok := l.failed[version]; ok {
		return nil, err
	}
	path := ""
	if l.cacheDir != "" {
		path = filepath.Join(l.cacheDir, "azure-native-tags-"+version+".json")
		if raw, err := os.ReadFile(path); err == nil {
			var t azureNativeTags
			if json.Unmarshal(raw, &t) == nil && t.Version == version && t.Types != nil {
				l.versions[version] = &t
				return &t, nil
			}
		}
	}
	t, err := distillAzureNative(ctx, prov, version)
	if err != nil {
		l.failed[version] = err
		return nil, err
	}
	l.versions[version] = t
	if path != "" {
		if raw, err := json.Marshal(t); err == nil {
			// A cache that cannot be written is read from the provider again next run; nothing else changes.
			if os.MkdirAll(l.cacheDir, 0o700) == nil {
				tmp := path + ".tmp"
				if os.WriteFile(tmp, raw, 0o600) == nil {
					_ = os.Rename(tmp, path)
				}
			}
		}
	}
	return t, nil
}

// The parts of a Pulumi package schema plunge reads.
type pulumiSchemaProperty struct {
	Type                 string                `json:"type"`
	Ref                  string                `json:"$ref"`
	AdditionalProperties *pulumiSchemaProperty `json:"additionalProperties"`
	WillReplaceOnChanges bool                  `json:"willReplaceOnChanges"`
}

type pulumiSchemaResources struct {
	Resources map[string]struct {
		InputProperties map[string]*pulumiSchemaProperty `json:"inputProperties"`
	} `json:"resources"`
}

func distillAzureNative(ctx context.Context, prov plugin.Provider, version string) (*azureNativeTags, error) {
	resp, err := prov.GetSchema(ctx, plugin.GetSchemaRequest{})
	if err != nil {
		return nil, fmt.Errorf("the azure-native provider's schema could not be read: %w", err)
	}
	if len(resp.Schema) == 0 {
		return nil, errors.New("the azure-native provider served an empty schema")
	}
	var s pulumiSchemaResources
	if err := json.Unmarshal(resp.Schema, &s); err != nil {
		return nil, fmt.Errorf("the azure-native provider's schema does not parse: %w", err)
	}
	t := &azureNativeTags{Version: version, Types: map[string]TagShape{}, CreateOnly: map[string]bool{}}
	for tok, r := range s.Resources {
		tags, ok := r.InputProperties["tags"]
		switch {
		case !ok || tags == nil:
			t.Types[tok] = ShapeNone
		case tags.Type == "object" && tags.AdditionalProperties != nil && tags.AdditionalProperties.Type == "string":
			t.Types[tok] = ShapeMap
			if tags.WillReplaceOnChanges {
				t.CreateOnly[tok] = true
			}
		default:
			t.Types[tok] = ShapeUnsupported
		}
	}
	return t, nil
}

// azureNative fills info from the azure-native schema. When the schema cannot be read, the earlier rule stands: a
// marker is written only where the program sets the tags map (inject.go), and the type is otherwise unknown.
func (c *catalog) azureNative(ctx context.Context, prov plugin.Provider, info *TypeInfo) {
	info.Property = "tags"
	fallback := func(why string) {
		info.Shape = ShapeUnknown
		info.Reason = why + "; a marker is written only where the program sets the tags map"
		info.azureNativeFallback = true
	}
	if prov == nil {
		fallback("the azure-native provider plugin was not loaded, so its schema could not be read")
		return
	}
	version := providerVersion(ctx, prov)
	if version == "" {
		fallback("the azure-native provider version is not known")
		return
	}
	t, err := c.azureNativeTags.load(ctx, prov, version)
	if err != nil {
		fallback(err.Error())
		return
	}
	shape, ok := t.Types[string(info.Type)]
	switch {
	case !ok:
		info.Shape = ShapeUnknown
		info.Reason = fmt.Sprintf("azure-native %s's schema does not name this type", version)
	case shape == ShapeNone:
		info.Shape, info.Reason = ShapeNone, info.Type.String()+" has no tags input in azure-native "+version
	case shape == ShapeUnsupported:
		info.Shape = ShapeUnsupported
		info.Reason = info.Type.String() + "'s tags input is not a map of strings in azure-native " + version
	default:
		info.Shape, info.CreateOnly = ShapeMap, t.CreateOnly[string(info.Type)]
	}
}
