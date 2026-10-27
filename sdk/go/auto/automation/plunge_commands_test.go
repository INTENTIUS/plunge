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

// Argv tests for plunge's hand-written automation commands (INTENTIUS/plunge#29). A fake
// PulumiCommand records what API.run hands it; no CLI is started.

package automation

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/sdk/v3/go/auto/automation/optplungedrift"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/automation/optplungestackadopt"
)

type recordingCommand struct {
	workdir string
	env     []string
	args    []string
}

func (r *recordingCommand) Run(
	_ context.Context,
	workdir string,
	_ io.Reader,
	_ []io.Writer,
	_ []io.Writer,
	additionalEnv []string,
	args ...string,
) (string, string, int, error) {
	r.workdir = workdir
	r.env = additionalEnv
	r.args = args
	return "", "", 0, nil
}

func TestPlungeDriftArgvMinimal(t *testing.T) {
	t.Parallel()
	cmd := &recordingCommand{}
	_, err := New(cmd).Drift(context.Background(),
		optplungedrift.Resource("urn:pulumi:dev::proj::aws:s3/bucket:Bucket::b"),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"drift", "--resource", "urn:pulumi:dev::proj::aws:s3/bucket:Bucket::b"}, cmd.args)
}

func TestPlungeDriftArgvAllFlags(t *testing.T) {
	t.Parallel()
	cmd := &recordingCommand{}
	_, err := New(cmd).Drift(context.Background(),
		optplungedrift.Cwd("/work"),
		optplungedrift.AdditionalEnv(map[string]string{"PULUMI_BACKEND_URL": "plunge+file:///tmp/b"}),
		optplungedrift.Color("never"),
		optplungedrift.DisableIntegrityChecking(true),
		optplungedrift.FullyQualifyStackNames(true),
		optplungedrift.Logflow(true),
		optplungedrift.Logtostderr(true),
		optplungedrift.Member("api"),
		optplungedrift.Memprofilerate(7),
		optplungedrift.OtelTraces("file:///tmp/t.json"),
		optplungedrift.Profiling("prof"),
		optplungedrift.Resource("urn:r"),
		optplungedrift.Stack("dev"),
		optplungedrift.Stream(true),
		optplungedrift.Tracing("file:/tmp/trace"),
		optplungedrift.TracingHeader("h"),
		optplungedrift.Verbose(3),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"drift",
		"--color", "never",
		"--disable-integrity-checking",
		"--fully-qualify-stack-names",
		"--logflow",
		"--logtostderr",
		"--member", "api",
		"--memprofilerate", "7",
		"--otel-traces", "file:///tmp/t.json",
		"--profiling", "prof",
		"--resource", "urn:r",
		"--stack", "dev",
		"--stream",
		"--tracing", "file:/tmp/trace",
		"--tracing-header", "h",
		"--verbose", "3",
	}, cmd.args)
	assert.Equal(t, "/work", cmd.workdir)
	assert.Equal(t, []string{"PULUMI_BACKEND_URL=plunge+file:///tmp/b"}, cmd.env)
}

func TestPlungeStackAdoptArgvWithoutYes(t *testing.T) {
	t.Parallel()
	cmd := &recordingCommand{}
	_, err := New(cmd).StackAdopt(context.Background(),
		optplungestackadopt.Json(true),
		optplungestackadopt.Stack("dev"),
	)
	require.NoError(t, err)
	// Without Yes the command only classifies, so --yes must not appear.
	assert.Equal(t, []string{"stack", "adopt", "--json", "--stack", "dev"}, cmd.args)
	assert.NotContains(t, cmd.args, "--yes")
	assert.NotContains(t, cmd.args, "yes")
}

func TestPlungeStackAdoptArgvYes(t *testing.T) {
	t.Parallel()
	cmd := &recordingCommand{}
	_, err := New(cmd).StackAdopt(context.Background(),
		optplungestackadopt.Yes(true),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"stack", "adopt", "--yes"}, cmd.args)
}

func TestPlungeStackAdoptArgvAllFlags(t *testing.T) {
	t.Parallel()
	cmd := &recordingCommand{}
	_, err := New(cmd).StackAdopt(context.Background(),
		optplungestackadopt.Cwd("/work"),
		optplungestackadopt.Color("raw"),
		optplungestackadopt.DisableIntegrityChecking(true),
		optplungestackadopt.File("deployment.json"),
		optplungestackadopt.FullyQualifyStackNames(true),
		optplungestackadopt.Json(true),
		optplungestackadopt.Logflow(true),
		optplungestackadopt.Logtostderr(true),
		optplungestackadopt.Memprofilerate(2),
		optplungestackadopt.OtelTraces("grpc://127.0.0.1:4317"),
		optplungestackadopt.Profiling("prof"),
		optplungestackadopt.Stack("org/proj/dev"),
		optplungestackadopt.Tracing("file:/tmp/trace"),
		optplungestackadopt.TracingHeader("h"),
		optplungestackadopt.Verbose(9),
		optplungestackadopt.Yes(true),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"stack", "adopt",
		"--color", "raw",
		"--disable-integrity-checking",
		"--file", "deployment.json",
		"--fully-qualify-stack-names",
		"--json",
		"--logflow",
		"--logtostderr",
		"--memprofilerate", "2",
		"--otel-traces", "grpc://127.0.0.1:4317",
		"--profiling", "prof",
		"--stack", "org/proj/dev",
		"--tracing", "file:/tmp/trace",
		"--tracing-header", "h",
		"--verbose", "9",
		"--yes",
	}, cmd.args)
	assert.Equal(t, "/work", cmd.workdir)
}
