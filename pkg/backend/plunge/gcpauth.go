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

// How plunge's GCP listers authenticate when the stack's google provider names custom endpoints
// (INTENTIUS/plunge#42). plunge#27 keeps every listing call on the stack's own endpoints; this file keeps the token
// fetches there too. A credentials JSON mints its tokens at the token_uri it names (oauth2.googleapis.com), and
// Application Default Credentials look in GOOGLE_APPLICATION_CREDENTIALS, gcloud's application-default login and
// the GCE metadata server (metadata.google.internal, 169.254.169.254). None of those may be reached for a stack
// that points its provider at an emulator, so when any custom endpoint is named:
//
//   - accessToken (or GOOGLE_OAUTH_ACCESS_TOKEN) is sent as given: a static token fetches nothing.
//   - otherwise, when every named endpoint is on a loopback host (floci-gcp on localhost), the calls carry no
//     credential at all. floci-gcp takes an anonymous call, and the credentials JSON or ADC in reach is never read.
//   - otherwise the listing is skipped with a warning (a SkippedListing), since the only credential left would
//     fetch its token from Google.
//   - impersonateServiceAccount mints through iamCredentialsCustomEndpoint (plunge#27 already skips when it is
//     unset) and only when that endpoint is on a loopback host; its base credential is the one above.
//
// The client built here never goes through google.golang.org/api's credential lookup: its transport is
// listerTransport (http.DefaultTransport when nil), wrapped only by a static or impersonated token source.
//
// What INTENTIUS/plunge#39 must carry when it swaps pkg/backend/plunge/gcpsweep for choudoufu's public package: this
// file stays as it is, and the public package must take a caller-built *http.Client for Cloud Asset (the role
// gcpsweep.NewWithHTTPClient plays here) and must not look up a credential when given one. gcpsweep.RefusedMarks
// and gcpsweep.SourceCaller are plunge's additions too (gcpsweep/plunge.go). Without that seam the asset lister
// has to skip in custom-endpoint mode rather than fall back to the package's own credential lookup.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/oauth2"
	iamcredentials "google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/option"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/gcpsweep"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
)

// gcpDefaultScopes are the google provider's default OAuth scopes, for an impersonated token.
var gcpDefaultScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
}

// gcpCustomEndpointURLs is gcpCustomEndpoints with each endpoint's value: the provider's "<service>CustomEndpoint"
// inputs and the GOOGLE_*_CUSTOM_ENDPOINT variables the provider reads.
func gcpCustomEndpointURLs(inputs map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range inputs {
		if v != "" {
			out[k] = v
		}
	}
	for _, n := range envNamesWith("GOOGLE_", "_CUSTOM_ENDPOINT") {
		out[n] = os.Getenv(n)
	}
	return out
}

// gcpProviderEndpoints reads the custom endpoint inputs off a google provider's configuration.
func gcpProviderEndpoints(req deploy.BindRequest) map[string]string {
	in := map[string]string{}
	for k := range req.ProviderInputs {
		if strings.HasSuffix(string(k), "CustomEndpoint") {
			in[string(k)] = providerString(req.ProviderInputs, string(k))
		}
	}
	return gcpCustomEndpointURLs(in)
}

// isLoopbackEndpoint is whether an endpoint URL (or a bare host[:port]) names localhost or a loopback address,
// by plunge#41's isLoopbackHost (armendpoints.go).
func isLoopbackEndpoint(raw string) bool {
	s := strings.TrimSpace(raw)
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return isLoopbackHost(s)
}

// gcpLocalAuth is how a custom-endpoint stack's GCP calls authenticate, decided before any client is built.
type gcpLocalAuth struct {
	// token is the static access token, or "" for anonymous calls.
	token string
	// impersonate and iamEndpoint are set when the token is minted through IAM Credentials.
	impersonate string
	delegates   []string
	iamEndpoint string
	scopes      []string
}

// String is what diagnostics name the credential as.
func (g gcpLocalAuth) String() string {
	base := "no credential (the provider's custom endpoints are all on loopback hosts)"
	if g.token != "" {
		base = "access_token"
	}
	if g.impersonate != "" {
		return base + " impersonating " + g.impersonate
	}
	return base
}

// gcpLocalAuthFor decides how req's GCP calls authenticate when its provider names custom endpoints: ok is false
// when it names none (the provider's own credential chain applies, as before), and a non-nil error is the
// SkippedListing for a configuration whose only credential would fetch a token from a non-local host. service is
// what the skipped listing would have called. a has had the environment applied.
func gcpLocalAuthFor(req deploy.BindRequest, a gcpsweep.Attrs, service string) (auth gcpLocalAuth, ok bool, err error) {
	eps := gcpProviderEndpoints(req)
	if len(eps) == 0 {
		return gcpLocalAuth{}, false, nil
	}
	names := make([]string, 0, len(eps))
	var nonLocal []string
	for n, v := range eps {
		names = append(names, n)
		if !isLoopbackEndpoint(v) {
			nonLocal = append(nonLocal, n)
		}
	}
	sort.Strings(names)
	sort.Strings(nonLocal)

	if refused := a.RefusedMarks(); len(refused) > 0 {
		return gcpLocalAuth{}, true, fmt.Errorf("the google provider sets %s from a sensitive value, which the "+
			"lister does not read", strings.Join(refused, " and "))
	}
	switch {
	case a.AccessToken != "":
		token, rerr := gcpPathOrContents(a.AccessToken)
		if rerr != nil {
			return gcpLocalAuth{}, true, fmt.Errorf("reading access_token: %w", rerr)
		}
		auth.token = strings.TrimSpace(token)
	case len(nonLocal) > 0:
		what := "Application Default Credentials (GOOGLE_APPLICATION_CREDENTIALS, gcloud's login or the GCE " +
			"metadata server)"
		if a.Credentials != "" {
			what = "a credentials JSON, whose tokens come from Google's OAuth endpoint"
		}
		if a.ExternalCredentials {
			what = "external credentials, exchanged at Google's STS"
		}
		return gcpLocalAuth{}, true, skipped(req.URN, service, "the google provider sets custom endpoints (%s), "+
			"%s not on a loopback host, and authenticates with %s; plunge sends GCP calls with no credential only "+
			"when every custom endpoint is local, so set accessToken (or GOOGLE_OAUTH_ACCESS_TOKEN) for an "+
			"endpoint that wants a token", strings.Join(names, ", "), strings.Join(nonLocal, ", "), what)
	}
	if a.ImpersonateServiceAccount != "" {
		if a.IAMCredentialsEndpoint == "" || !isLoopbackEndpoint(a.IAMCredentialsEndpoint) {
			return gcpLocalAuth{}, true, skipped(req.URN, "iamcredentials.googleapis.com", "the google provider "+
				"impersonates %s and sets custom endpoints (%s), and the impersonated token would be minted at %q, "+
				"which is not a loopback host", a.ImpersonateServiceAccount, strings.Join(names, ", "),
				a.IAMCredentialsEndpoint)
		}
		auth.impersonate = a.ImpersonateServiceAccount
		auth.delegates = append([]string(nil), a.ImpersonateDelegates...)
		auth.iamEndpoint = a.IAMCredentialsEndpoint
		auth.scopes = a.Scopes
		if len(auth.scopes) == 0 {
			auth.scopes = gcpDefaultScopes
		}
	}
	return auth, true, nil
}

// cacheKey keys the lister's client cache.
func (g gcpLocalAuth) cacheKey() string {
	return "local|" + g.token + "|" + g.impersonate + "|" + g.iamEndpoint
}

// client builds the HTTP client: listerTransport, wrapped by the static or impersonated token when there is one.
// Nothing here looks up a credential.
func (g gcpLocalAuth) client(ctx context.Context) (*http.Client, error) {
	base := listerTransport
	if base == nil {
		base = http.DefaultTransport
	}
	var ts oauth2.TokenSource
	if g.token != "" {
		ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: g.token})
	}
	baseClient := &http.Client{Transport: base}
	if ts != nil {
		baseClient = &http.Client{Transport: &oauth2.Transport{Source: ts, Base: base}}
	}
	if g.impersonate == "" {
		return baseClient, nil
	}
	iam, err := iamcredentials.NewService(ctx, option.WithHTTPClient(baseClient),
		option.WithEndpoint(gcpServiceRoot(g.iamEndpoint)))
	if err != nil {
		return nil, fmt.Errorf("building the IAM Credentials client to impersonate %s: %w", g.impersonate, err)
	}
	minted := oauth2.ReuseTokenSource(nil, gcpImpersonated{
		iam: iam, target: g.impersonate, delegates: g.delegates, scopes: g.scopes,
	})
	return &http.Client{Transport: &oauth2.Transport{Source: minted, Base: base}}, nil
}

// gcpImpersonated mints target's token through generateAccessToken at the stack's IAM Credentials endpoint, as
// gcpsweep's impersonated does; written here so the custom-endpoint path does not depend on the copy (plunge#39).
type gcpImpersonated struct {
	iam       *iamcredentials.Service
	target    string
	delegates []string
	scopes    []string
}

func (i gcpImpersonated) Token() (*oauth2.Token, error) {
	req := &iamcredentials.GenerateAccessTokenRequest{Scope: i.scopes, Lifetime: "3600s"}
	for _, d := range i.delegates {
		req.Delegates = append(req.Delegates, "projects/-/serviceAccounts/"+d)
	}
	resp, err := i.iam.Projects.ServiceAccounts.GenerateAccessToken("projects/-/serviceAccounts/"+i.target, req).Do()
	if err != nil {
		return nil, fmt.Errorf("impersonating %s: %w", i.target, err)
	}
	expiry, err := time.Parse(time.RFC3339, resp.ExpireTime)
	if err != nil {
		return nil, fmt.Errorf("impersonating %s: unreadable expireTime %q: %w", i.target, resp.ExpireTime, err)
	}
	return &oauth2.Token{AccessToken: resp.AccessToken, Expiry: expiry}, nil
}

// gcpServiceRoot is gcpsweep's serviceRoot: a provider custom endpoint with its trailing "v1" dropped.
func gcpServiceRoot(endpoint string) string {
	e := strings.TrimRight(endpoint, "/")
	e = strings.TrimSuffix(e, "/v1")
	return e + "/"
}

// gcpPathOrContents is the provider's pathOrContents: a value naming a file that exists is that file's contents.
func gcpPathOrContents(poc string) (string, error) {
	if poc == "" {
		return poc, nil
	}
	path := poc
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path, err
		}
		path = filepath.Join(home, path[1:])
	}
	if _, err := os.Stat(path); err == nil {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return poc, nil
}
