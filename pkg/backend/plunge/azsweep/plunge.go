// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"context"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
)

// The marker vocabulary, plunge's in place of choudoufu's internal/live/markers (see NOTICE).
var markersTagEstate = markers.TagStack

// Credential is [Attrs.credential], exported for plunge's per-type ARM lister
// (pkg/backend/plunge/listers.go), which lists by tag through ARM itself
// rather than through Resource Graph's eventually consistent index.
func (a Attrs) Credential(ctx context.Context, who string) (azcore.TokenCredential, Endpoints, Credentials, error) {
	return a.WithEnv(os.Getenv).credential(ctx, who)
}

// Scope is the token scope for an ARM audience.
func Scope(audience string) string { return scope(audience) }
