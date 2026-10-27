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

	cloudasset "google.golang.org/api/cloudasset/v1"
	"google.golang.org/api/option"
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

// SourceCaller is a client whose credential the caller built: [NewWithHTTPClient]'s, where plunge's lister glue
// (pkg/backend/plunge/gcpauth.go) has already decided what, if anything, authenticates the calls. Plunge's own
// addition (INTENTIUS/plunge#42).
const SourceCaller CredentialSource = "the caller's client"

// RefusedMarks are the arguments a refuses a client over ([Attrs.refusedMarks]), for a caller building its own
// client. Plunge's own addition (INTENTIUS/plunge#42).
func (a Attrs) RefusedMarks() []string { return a.refusedMarks() }

// NewWithHTTPClient is [New] over a client the caller built: every call goes through c and nothing here looks up a
// credential, so no credentials file is read, Application Default Credentials are not searched and the metadata
// server is not asked. creds is what diagnostics name the credential as. a's CloudAssetEndpoint, when set, is the
// endpoint called. Plunge's own addition (INTENTIUS/plunge#42): plunge uses it when the stack's provider names
// custom endpoints, so that no token source reaches Google's public auth endpoints.
func NewWithHTTPClient(ctx context.Context, a Attrs, c *http.Client, creds Credentials) (*Client, error) {
	a = a.WithEnv(os.Getenv)
	if refused := a.refusedMarks(); len(refused) > 0 {
		return nil, fmt.Errorf("the google provider sets %s from a sensitive value, which the lister does not read",
			strings.Join(refused, " and "))
	}
	opts := []option.ClientOption{option.WithHTTPClient(c)}
	if a.CloudAssetEndpoint != "" {
		opts = append(opts, option.WithEndpoint(serviceRoot(a.CloudAssetEndpoint)))
	}
	svc, err := cloudasset.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("building the Cloud Asset client (%s): %w", creds, err)
	}
	return &Client{svc: svc, attrs: a, creds: creds}, nil
}
