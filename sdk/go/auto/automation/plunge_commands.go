// Copyright 2026, INTENTIUS.
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

// plunge's own commands for the automation API, written by hand. The generator
// (sdk/go/tools/automation) writes api.go, commands.go and the opt* packages from upstream's CLI spec;
// it never writes this file or the optplunge* packages, so an upstream regeneration of the package
// leaves them alone (INTENTIUS/plunge#21). They go through the same API.run as the generated methods.

package automation

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/auto/automation/base"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/automation/optplungedrift"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/automation/optplungestackadopt"
)

// Drift corresponds to `plunge drift`.
//
// Read one resource's drift with one provider Read.
//
// Reads the resource's record and its provider's record from the plunge backend by key, without
// assembling the stack's snapshot and without a stack-wide refresh, configures the provider from its
// record, calls Read once, and compares the live outputs with the recorded ones. With Stream the
// result is chant's drift record v1 NDJSON on stdout. Needs a plunge+<url> backend.
func (a *API) Drift(
	ctx context.Context,
	opts ...optplungedrift.Option,
) (base.CommandResult, error) {
	o := optplungedrift.Options{}
	for _, opt := range opts {
		opt(&o)
	}

	final := []string{"drift"}

	if o.Color != "" {
		final = append(final, "--color", fmt.Sprint(o.Color))
	}

	if o.DisableIntegrityChecking {
		final = append(final, "--disable-integrity-checking")
	}

	if o.FullyQualifyStackNames {
		final = append(final, "--fully-qualify-stack-names")
	}

	if o.Logflow {
		final = append(final, "--logflow")
	}

	if o.Logtostderr {
		final = append(final, "--logtostderr")
	}

	if o.Member != "" {
		final = append(final, "--member", fmt.Sprint(o.Member))
	}

	if o.Memprofilerate != 0 {
		final = append(final, "--memprofilerate", fmt.Sprint(o.Memprofilerate))
	}

	if o.OtelTraces != "" {
		final = append(final, "--otel-traces", fmt.Sprint(o.OtelTraces))
	}

	if o.Profiling != "" {
		final = append(final, "--profiling", fmt.Sprint(o.Profiling))
	}

	final = append(final, "--resource", fmt.Sprint(o.Resource))

	if o.Stack != "" {
		final = append(final, "--stack", fmt.Sprint(o.Stack))
	}

	if o.Stream {
		final = append(final, "--stream")
	}

	if o.Tracing != "" {
		final = append(final, "--tracing", fmt.Sprint(o.Tracing))
	}

	if o.TracingHeader != "" {
		final = append(final, "--tracing-header", fmt.Sprint(o.TracingHeader))
	}

	if o.Verbose != 0 {
		final = append(final, "--verbose", fmt.Sprint(o.Verbose))
	}

	return a.run(ctx, base.BaseOptions{
		Cwd:           o.Cwd,
		AdditionalEnv: o.AdditionalEnv,
		Stdout:        o.Stdout,
		Stderr:        o.Stderr,
		Stdin:         o.Stdin,
	}, final)
}

// StackAdopt corresponds to `plunge stack adopt`.
//
// Adopt a stock Pulumi stack into plunge's per-resource records.
//
// Runs against a plunge backend (plunge+<diy url>). With JSON the classification is printed as one
// JSON document (docs/plunge/ADOPT.md); without Yes nothing is written. A refused adoption prints
// the document with its refusals and exits non-zero. Yes is an option here, unlike the generated
// commands, which always pass --yes: it is what turns a classification into an adoption.
func (a *API) StackAdopt(
	ctx context.Context,
	opts ...optplungestackadopt.Option,
) (base.CommandResult, error) {
	o := optplungestackadopt.Options{}
	for _, opt := range opts {
		opt(&o)
	}

	final := []string{"stack", "adopt"}

	if o.Color != "" {
		final = append(final, "--color", fmt.Sprint(o.Color))
	}

	if o.DisableIntegrityChecking {
		final = append(final, "--disable-integrity-checking")
	}

	if o.File != "" {
		final = append(final, "--file", fmt.Sprint(o.File))
	}

	if o.FullyQualifyStackNames {
		final = append(final, "--fully-qualify-stack-names")
	}

	if o.Json {
		final = append(final, "--json")
	}

	if o.Logflow {
		final = append(final, "--logflow")
	}

	if o.Logtostderr {
		final = append(final, "--logtostderr")
	}

	if o.Memprofilerate != 0 {
		final = append(final, "--memprofilerate", fmt.Sprint(o.Memprofilerate))
	}

	if o.OtelTraces != "" {
		final = append(final, "--otel-traces", fmt.Sprint(o.OtelTraces))
	}

	if o.Profiling != "" {
		final = append(final, "--profiling", fmt.Sprint(o.Profiling))
	}

	if o.Stack != "" {
		final = append(final, "--stack", fmt.Sprint(o.Stack))
	}

	if o.Tracing != "" {
		final = append(final, "--tracing", fmt.Sprint(o.Tracing))
	}

	if o.TracingHeader != "" {
		final = append(final, "--tracing-header", fmt.Sprint(o.TracingHeader))
	}

	if o.Verbose != 0 {
		final = append(final, "--verbose", fmt.Sprint(o.Verbose))
	}

	if o.Yes {
		final = append(final, "--yes")
	}

	return a.run(ctx, base.BaseOptions{
		Cwd:           o.Cwd,
		AdditionalEnv: o.AdditionalEnv,
		Stdout:        o.Stdout,
		Stderr:        o.Stderr,
		Stdin:         o.Stdin,
	}, final)
}
