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

package deploy

import (
	pkgresource "github.com/pulumi/pulumi/pkg/v3/resource"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi-internal/gsync"
)

// NewBuiltinProvider is the provider of the `pulumi` package (stack references, stashes, getResource) for a caller
// outside a deployment, for providers.NewRegistry's builtins argument. plunge's per-resource drift read
// (pkg/backend/plunge/drift.go, INTENTIUS/plunge#14) loads a StackReference's provider through it. There is no
// deployment, so the provider sees no resources registered or read in one. Upstream code does not call it.
func NewBuiltinProvider(backendClient BackendClient, d diag.Sink) plugin.Provider {
	return newBuiltinProvider(backendClient,
		&gsync.Map[resource.URN, *pkgresource.State]{}, &gsync.Map[resource.URN, *pkgresource.State]{}, d)
}
