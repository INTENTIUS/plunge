// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	htransport "google.golang.org/api/transport/http"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
)

// The marker vocabulary, plunge's in place of choudoufu's internal/live/markers (see NOTICE).
var (
	markersTagEstate  = markers.TagStack
	markersTagAddress = markers.TagURN
)

// HTTPClient is an http.Client authenticated as a's credential, the same one [New] builds the Cloud Asset client
// from, for plunge's per-service GCP listers (package gcplist), which call each service's REST list directly. It is
// plunge's own addition; choudoufu's sweep has no such caller.
func HTTPClient(ctx context.Context, a Attrs) (*http.Client, error) {
	a = a.WithEnv(os.Getenv)
	if refused := a.refusedMarks(); len(refused) > 0 {
		return nil, fmt.Errorf("the google provider sets %s from a sensitive value, which the lister does not read",
			strings.Join(refused, " and "))
	}
	if a.ExternalCredentials {
		return nil, fmt.Errorf("the google provider authenticates through external credentials, which the lister " +
			"cannot use; configure credentials, accessToken or Application Default Credentials")
	}
	// The Cloud Asset endpoint is not this client's concern: each lister call names its own service's URL.
	a.CloudAssetEndpoint = ""
	opts, err := a.clientOptions(ctx)
	if err != nil {
		return nil, err
	}
	c, _, err := htransport.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("building the GCP HTTP client (%s): %w", a.Auth(), err)
	}
	return c, nil
}
