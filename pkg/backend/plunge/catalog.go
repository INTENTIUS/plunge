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

// Where a marker goes on each resource type (INTENTIUS/plunge#7). Pulumi's schema has no "taggable" concept, so this
// is per family and schema-driven, as INTENTIUS/plunge#2 lays out:
//
//   - Bridged providers (pulumi-aws, pulumi-azure, pulumi-gcp) serve their Terraform schema through
//     GetMapping("terraform"). The type's Terraform resource says whether it has a tags (labels, for Google)
//     argument, whether it is a map, and whether changing it forces a new resource.
//   - aws-native serves nothing over the wire; its committed metadata.json at the provider's version tag says the
//     tags property, its style and whether it is create-only (package awsnative).
//   - Kubernetes objects all carry metadata.labels.
//   - azure-native's schema is read once per provider version and distilled to each type's tags shape, which is
//     cached on disk (azurenative.go, INTENTIUS/plunge#15). When it cannot be read, a type is marked only where the
//     program already sets its tags map, which proves the type has one; elsewhere it is recorded as unknown.
//
// Map-style tags are the shape written. aws-native's key/value arrays are written too, since they are that
// provider's usual shape. Any other shape is recorded and left alone, never guessed at.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/intentius/choudoufu/pkg/identity"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/awsnative"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/registry"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// Family is a provider family: the unit marker placement, import identity and listing are decided per.
type Family string

const (
	FamilyAWS         Family = "aws"          // pulumi-aws, bridged from hashicorp/aws
	FamilyAWSNative   Family = "aws-native"   // pulumi-aws-native, generated from CloudFormation
	FamilyAzure       Family = "azure"        // pulumi-azure, bridged from hashicorp/azurerm
	FamilyAzureNative Family = "azure-native" // pulumi-azure-native, generated from ARM
	FamilyGoogle      Family = "gcp"          // pulumi-gcp, bridged from hashicorp/google
	FamilyKubernetes  Family = "kubernetes"   // pulumi-kubernetes
	FamilyOther       Family = ""
)

// familyOf is the family a type's package belongs to.
func familyOf(t tokens.Type) Family {
	switch Family(t.Package()) {
	case FamilyAWS, FamilyAWSNative, FamilyAzure, FamilyAzureNative, FamilyGoogle, FamilyKubernetes:
		return Family(t.Package())
	}
	return FamilyOther
}

// bridged reports whether a family's provider is a Terraform bridge that serves GetMapping.
func (f Family) bridged() bool { return f == FamilyAWS || f == FamilyAzure || f == FamilyGoogle }

// encoding is how the family's markers are written.
func (f Family) encoding() markers.Encoding {
	if f == FamilyGoogle || f == FamilyKubernetes {
		return markers.EncodingLabels
	}
	return markers.EncodingTags
}

// TagShape is what a type's tags property looks like.
type TagShape string

const (
	// ShapeMap is a map of strings: tags = {key: value}.
	ShapeMap TagShape = "map"
	// ShapeKeyValueArray is aws-native's list of {key, value} objects.
	ShapeKeyValueArray TagShape = "key-value-array"
	// ShapeNone is a type with no tags at all. Its ownership is carried by its record.
	ShapeNone TagShape = "none"
	// ShapeUnsupported is a tags property of a shape plunge does not write.
	ShapeUnsupported TagShape = "unsupported"
	// ShapeUnknown is a type whose schema could not be read.
	ShapeUnknown TagShape = "unknown"
)

// TypeInfo is what plunge knows about one resource type's markers and identity.
type TypeInfo struct {
	Type   tokens.Type
	Family Family
	// Property is the input property the markers go in ("tags", "labels"); for Kubernetes, "metadata" (the labels
	// map inside it).
	Property string
	Shape    TagShape
	// CreateOnly reports that the tags can only be set at create: adding a marker to an existing object replaces
	// it, so adoption refuses the type and the injector never adds one to an existing object.
	CreateOnly bool
	// TFType is a bridged type's Terraform resource type.
	TFType string
	// CFNType is the CloudFormation type an AWS type is listed through: aws-native's own, or the one choudoufu's
	// registry joins a pulumi-aws type to. Empty when there is none.
	CFNType string
	// Listable reports that Cloud Control can list CFNType with no parent input.
	Listable bool
	// PrimaryIdentifier is aws-native's primary identifier property list.
	PrimaryIdentifier []string
	// Identity is pkg/identity's row for TFType, when it has one.
	Identity *identity.TypeIdentity
	// Markerless reports that pkg/identity classes TFType as unable to carry a marker.
	Markerless bool
	// Reason says why Shape is none, unsupported or unknown.
	Reason string

	// Autoname is what plunge knows about the type's autonamed property (autoname.go).
	Autoname AutonameInfo

	// azureNativeFallback reports that azure-native's schema could not be read, so a program-set tags map is the
	// only evidence the type has one.
	azureNativeFallback bool
}

// AutonameInfo is what the schema says about a type's autonamed property.
type AutonameInfo struct {
	// Property is the Pulumi input the provider autonames ("name", "bucket", "roleName"); empty when the schema
	// names none.
	Property string
	// MaxLength is the longest name the schema allows, 0 when it does not say. Suffix is what the provider may
	// append to it (aws-native's ".fifo"), so a name is cut short enough to leave room for it.
	MaxLength int
	Suffix    string
	// Unique reports schema evidence that the cloud refuses a second object of the type with the same name in the
	// same scope. Evidence names it; Uncovered says why there is none.
	Unique    bool
	Evidence  string
	Uncovered string
}

// Encoding is how markers are written on the type.
func (i TypeInfo) Encoding() markers.Encoding { return i.Family.encoding() }

// catalog answers TypeInfo per type, reading each provider's schema once.
type catalog struct {
	awsNative       *awsnative.Loader
	azureNativeTags *azureNativeLoader

	mu       sync.Mutex
	mappings map[string]*bridgeMapping // by provider name and version
}

func newCatalog() *catalog {
	return &catalog{
		awsNative: awsnative.Default, azureNativeTags: newAzureNativeLoader(), mappings: map[string]*bridgeMapping{},
	}
}

// lookup returns what is known about t. prov is the plugin that manages t, nil when it is not loaded.
func (c *catalog) lookup(ctx context.Context, prov plugin.Provider, t tokens.Type) TypeInfo {
	info := TypeInfo{Type: t, Family: familyOf(t)}
	switch {
	case info.Family.bridged():
		c.bridged(ctx, prov, &info)
	case info.Family == FamilyAWSNative:
		c.native(ctx, prov, &info)
	case info.Family == FamilyKubernetes:
		kubernetesInfo(&info)
	case info.Family == FamilyAzureNative:
		c.azureNative(ctx, prov, &info)
	default:
		info.Shape = ShapeUnknown
		info.Reason = fmt.Sprintf("plunge has no marker placement for the %q provider family", t.Package())
	}
	return info
}

func providerVersion(ctx context.Context, prov plugin.Provider) string {
	if prov == nil {
		return ""
	}
	pi, err := prov.GetPluginInfo(ctx)
	if err != nil || pi.Version == nil {
		return ""
	}
	return pi.Version.String()
}

// The parts of a bridged provider's GetMapping("terraform") payload plunge reads: the bridge's
// MarshallableProvider (pulumi-terraform-bridge pkg/tfbridge/info/info.go, at e21841e).
type bridgeMapping struct {
	Provider *struct {
		Resources map[string]map[string]*bridgeSchema `json:"resources"`
	} `json:"provider"`
	Resources map[string]*struct {
		Tok    string                       `json:"tok"`
		Fields map[string]*bridgeSchemaInfo `json:"fields"`
	} `json:"resources"`

	// byToken maps a Pulumi type token to its Terraform type, built on first use.
	byToken map[string]string
}

// bridgeSchema is the bridge's MarshallableSchemaShim; Type is shim.ValueType, where 6 is TypeMap and 4 TypeString.
type bridgeSchema struct {
	Type     int  `json:"type"`
	Optional bool `json:"optional,omitempty"`
	Required bool `json:"required,omitempty"`
	Computed bool `json:"computed,omitempty"`
	ForceNew bool `json:"forceNew,omitempty"`
	Elem     *struct {
		Schema *bridgeSchema `json:"schema,omitempty"`
	} `json:"element,omitempty"`
}

type bridgeSchemaInfo struct {
	Name    string `json:"name,omitempty"`
	Default *struct {
		AutoNamed bool `json:"autonamed,omitempty"`
	} `json:"default,omitempty"`
}

const (
	shimTypeString = 4
	shimTypeMap    = 6
)

// mapping reads and caches prov's Terraform mapping.
func (c *catalog) mapping(ctx context.Context, prov plugin.Provider, pkg string) (*bridgeMapping, error) {
	key := pkg + "@" + providerVersion(ctx, prov)
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.mappings[key]; ok {
		if m == nil {
			return nil, errors.New("no mapping")
		}
		return m, nil
	}
	resp, err := prov.GetMapping(ctx, plugin.GetMappingRequest{Key: "terraform", Provider: pkg})
	if err == nil && len(resp.Data) == 0 {
		err = errors.New("the provider served an empty terraform mapping")
	}
	var m bridgeMapping
	if err == nil {
		err = json.Unmarshal(resp.Data, &m)
	}
	if err != nil {
		c.mappings[key] = nil
		return nil, err
	}
	m.byToken = map[string]string{}
	for tf, r := range m.Resources {
		if r != nil {
			m.byToken[r.Tok] = tf
		}
	}
	c.mappings[key] = &m
	return &m, nil
}

func (c *catalog) bridged(ctx context.Context, prov plugin.Provider, info *TypeInfo) {
	tagsArg := "tags"
	if info.Family == FamilyGoogle {
		tagsArg = "labels"
	}
	info.Property = tagsArg
	if prov == nil {
		info.Shape, info.Reason = ShapeUnknown, "the provider plugin was not loaded, so its schema could not be read"
		return
	}
	m, err := c.mapping(ctx, prov, string(info.Type.Package()))
	if err != nil {
		info.Shape = ShapeUnknown
		info.Reason = fmt.Sprintf("the %s provider's terraform mapping could not be read: %v", info.Type.Package(), err)
		return
	}
	tf, ok := m.byToken[string(info.Type)]
	if !ok {
		info.Shape, info.Reason = ShapeUnknown, "the provider's terraform mapping does not name this type"
		return
	}
	info.TFType = tf
	if row, ok := identity.LookupType(tf); ok {
		info.Identity = &row
	}
	info.Markerless = identity.IsMarkerless(tf)
	if info.Family == FamilyAWS {
		if roster, err := registry.Embedded(); err == nil {
			if cfn, ok := roster.EnumerationSource(tf); ok {
				info.CFNType, info.Listable = cfn, true
			} else if cfn, ok := roster.CloudControlType(tf); ok {
				info.CFNType = cfn
			}
		}
	}
	if fields := m.Resources[tf].Fields; fields != nil {
		if f, ok := fields[tagsArg]; ok && f != nil && f.Name != "" {
			info.Property = f.Name
		}
	}
	var schema map[string]*bridgeSchema
	if m.Provider != nil {
		schema = m.Provider.Resources[tf]
	}
	bridgedAutoname(info, m.Resources[tf].Fields, schema)
	s, ok := schema[tagsArg]
	switch {
	case schema == nil:
		info.Shape, info.Reason = ShapeUnknown, "the provider's terraform mapping carries no schema for "+tf
	case !ok || s == nil:
		info.Shape = ShapeNone
		info.Reason = fmt.Sprintf("%s has no %s argument", tf, tagsArg)
	case s.Type != shimTypeMap || (s.Elem != nil && s.Elem.Schema != nil && s.Elem.Schema.Type != shimTypeString):
		info.Shape = ShapeUnsupported
		info.Reason = fmt.Sprintf("%s's %s argument is not a map of strings", tf, tagsArg)
	case s.Computed && !s.Optional && !s.Required:
		info.Shape = ShapeNone
		info.Reason = fmt.Sprintf("%s's %s argument is computed only", tf, tagsArg)
	default:
		info.Shape = ShapeMap
		info.CreateOnly = s.ForceNew
	}
}

func (c *catalog) native(ctx context.Context, prov plugin.Provider, info *TypeInfo) {
	version := providerVersion(ctx, prov)
	if version == "" {
		info.Shape, info.Reason = ShapeUnknown, "the aws-native provider version is not known, so its metadata cannot be picked"
		return
	}
	md, err := c.awsNative.Load(ctx, version)
	if err != nil {
		info.Shape, info.Reason = ShapeUnknown, err.Error()
		return
	}
	nativeInfo(md, info)
}

// nativeInfo fills info from aws-native metadata.
func nativeInfo(md *awsnative.Metadata, info *TypeInfo) {
	r, ok := md.Resources[string(info.Type)]
	if !ok {
		info.Shape = ShapeUnknown
		info.Reason = fmt.Sprintf("aws-native %s's metadata does not name this type", md.Version)
		return
	}
	info.CFNType = r.CF
	info.Listable = r.HasListHandler
	info.PrimaryIdentifier = r.PrimaryIdentifier
	info.Property = r.TagsProperty
	switch r.TagsStyle {
	case "":
		info.Shape, info.Reason = ShapeNone, r.CF+" has no tags property"
	case awsnative.StyleStringMap:
		info.Shape = ShapeMap
	case awsnative.StyleKeyValueArray, awsnative.StyleKeyValueArrayCreateOnly:
		info.Shape = ShapeKeyValueArray
	default:
		info.Shape = ShapeUnsupported
		info.Reason = fmt.Sprintf("%s's tags are of style %q, which plunge does not write", r.CF, r.TagsStyle)
	}
	info.CreateOnly = r.TagsCreateOnly()
	nativeAutoname(r, info)
}

// kubernetesInfo places markers in metadata.labels, which every object kind has. The Helm release type is a
// release record, not an object with metadata, and is left alone.
func kubernetesInfo(info *TypeInfo) {
	info.Property = "metadata"
	mod := string(info.Type.Module())
	if strings.HasPrefix(mod, "kubernetes:helm.sh/") || strings.HasPrefix(mod, "kubernetes:yaml/") ||
		strings.HasPrefix(mod, "kubernetes:kustomize/") {
		info.Shape = ShapeUnsupported
		info.Reason = "a " + mod + " resource has no object metadata of its own to label"
		return
	}
	info.Shape = ShapeMap
}
