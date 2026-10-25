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

// Package awsnative reads pulumi-aws-native's per-type metadata: the CloudFormation type, the tags property and its
// style, whether tags are create-only, and the primary identifier (INTENTIUS/plunge#2, "aws-native metadata").
//
// aws-native serves none of this over the wire: GetSchema has no tag style or primary identifier and there is no
// GetMapping. The provider embeds it in its binary and commits it at every release tag as
// provider/cmd/pulumi-resource-aws-native/metadata.json, so plunge reads that file at the tag matching the
// provider version the program uses.
//
// Where it comes from, in order: the file EnvMetadata names (a full metadata.json or the distilled form); a copy
// cached under the plunge home for that version; the distilled copy of v1.83.1 compiled in here, when that is the
// version; and otherwise the release tag's file, fetched once and cached. A version none of those answers is
// refused by name, never guessed at.
package awsnative

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EnvMetadata names a metadata.json to read in place of the release tag's.
const EnvMetadata = "PLUNGE_AWS_NATIVE_METADATA"

// EmbeddedVersion is the aws-native release whose metadata is compiled in, distilled to the fields plunge reads.
// Regenerate metadata-<version>.json from that tag's provider/cmd/pulumi-resource-aws-native/metadata.json by
// keeping, per resource, cf, tagsProperty, tagsStyle, primaryIdentifier, hasListHandler, and createOnly
// restricted to the tags property.
const EmbeddedVersion = "1.83.1"

//go:embed metadata-1.83.1.json
var embedded []byte

// Tag styles, as metadata.json names them.
const (
	StyleStringMap     = "stringMap"
	StyleKeyValueArray = "keyValueArray"
	// StyleKeyValueArrayCreateOnly is a key/value array whose tags cannot change after create.
	StyleKeyValueArrayCreateOnly = "keyValueArrayCreateOnly"
)

// Resource is what plunge reads of one aws-native type.
type Resource struct {
	CF                string   `json:"cf"`
	TagsProperty      string   `json:"tagsProperty,omitempty"`
	TagsStyle         string   `json:"tagsStyle,omitempty"`
	CreateOnly        []string `json:"createOnly,omitempty"`
	PrimaryIdentifier []string `json:"primaryIdentifier,omitempty"`
	HasListHandler    bool     `json:"hasListHandler,omitempty"`
}

// TagsCreateOnly reports whether the type's tags can only be set when it is created: adding a marker to an
// existing one would replace it.
func (r Resource) TagsCreateOnly() bool {
	if r.TagsStyle == StyleKeyValueArrayCreateOnly {
		return true
	}
	for _, p := range r.CreateOnly {
		if p == r.TagsProperty {
			return true
		}
	}
	return false
}

// Metadata is one release's types, keyed by Pulumi token ("aws-native:s3:Bucket").
type Metadata struct {
	Version   string
	Resources map[string]Resource `json:"resources"`
}

// Parse reads a metadata.json, full or distilled.
func Parse(version string, data []byte) (*Metadata, error) {
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("plunge: reading aws-native %s metadata: %w", version, err)
	}
	if len(m.Resources) == 0 {
		return nil, fmt.Errorf("plunge: the aws-native %s metadata names no resources", version)
	}
	m.Version = version
	return &m, nil
}

// SourceURL is where a release's metadata.json is committed.
func SourceURL(version string) string {
	return "https://raw.githubusercontent.com/pulumi/pulumi-aws-native/v" + strings.TrimPrefix(version, "v") +
		"/provider/cmd/pulumi-resource-aws-native/metadata.json"
}

// Loader finds and caches metadata per version. The zero value is ready to use.
type Loader struct {
	// CacheDir holds fetched files, one per version. Empty is ~/.pulumi/plunge/aws-native.
	CacheDir string
	// Fetch gets a URL; nil is an HTTP GET with a one-minute timeout.
	Fetch func(ctx context.Context, url string) ([]byte, error)

	mu    sync.Mutex
	cache map[string]*Metadata
}

// Default is the process-wide loader.
var Default = &Loader{}

// UnavailableError refuses a version whose metadata could not be found.
type UnavailableError struct {
	Version string
	Err     error
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("plunge: no aws-native metadata for version %s, so its types' tag placement and identifiers "+
		"are unknown and no marker is written for them (fetching %s: %v); set %s to that release's metadata.json",
		e.Version, SourceURL(e.Version), e.Err, EnvMetadata)
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// Load returns version's metadata.
func (l *Loader) Load(ctx context.Context, version string) (*Metadata, error) {
	version = strings.TrimPrefix(version, "v")
	l.mu.Lock()
	defer l.mu.Unlock()
	if m, ok := l.cache[version]; ok {
		return m, nil
	}
	m, err := l.load(ctx, version)
	if err != nil {
		return nil, err
	}
	if l.cache == nil {
		l.cache = map[string]*Metadata{}
	}
	l.cache[version] = m
	return m, nil
}

func (l *Loader) load(ctx context.Context, version string) (*Metadata, error) {
	if path := os.Getenv(EnvMetadata); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("plunge: reading %s=%s: %w", EnvMetadata, path, err)
		}
		return Parse(version, data)
	}
	cached := filepath.Join(l.cacheDir(), version, "metadata.json")
	if data, err := os.ReadFile(cached); err == nil {
		return Parse(version, data)
	}
	if version == EmbeddedVersion {
		return Parse(version, embedded)
	}
	fetch := l.Fetch
	if fetch == nil {
		fetch = httpGet
	}
	data, err := fetch(ctx, SourceURL(version))
	if err != nil {
		return nil, &UnavailableError{Version: version, Err: err}
	}
	m, err := Parse(version, data)
	if err != nil {
		return nil, &UnavailableError{Version: version, Err: err}
	}
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err == nil {
		_ = os.WriteFile(cached, data, 0o600)
	}
	return m, nil
}

func (l *Loader) cacheDir() string {
	if l.CacheDir != "" {
		return l.CacheDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "plunge", "aws-native")
	}
	return filepath.Join(home, ".pulumi", "plunge", "aws-native")
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
