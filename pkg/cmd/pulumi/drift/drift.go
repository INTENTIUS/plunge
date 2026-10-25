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

// Package drift is `plunge drift`, one resource's drift from one provider Read (INTENTIUS/plunge#1).
package drift

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/display"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge"
	"github.com/pulumi/pulumi/pkg/v3/backend/secrets"
	cmdBackend "github.com/pulumi/pulumi/pkg/v3/cmd/pulumi/backend"
	cmdStack "github.com/pulumi/pulumi/pkg/v3/cmd/pulumi/stack"
	plungeVersion "github.com/pulumi/pulumi/pkg/v3/plunge"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	pkgWorkspace "github.com/pulumi/pulumi/pkg/v3/workspace"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/cmdutil"
)

// NewDriftCmd is `plunge drift --resource <urn> [--stream]`.
func NewDriftCmd(ws pkgWorkspace.Context, lm cmdBackend.LoginManager) *cobra.Command {
	var stackName, urn, member string
	var stream bool

	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Read one resource's drift with one provider Read",
		Long: "Read one resource's drift with one provider Read.\n" +
			"\n" +
			"Reads the resource's record and its provider's record from the plunge backend by key, without\n" +
			"assembling the stack's snapshot and without a stack-wide refresh, configures the provider from its\n" +
			"record, calls Read once, and compares the live outputs with the recorded ones. The program is not run,\n" +
			"so the program's inputs are not compared.\n" +
			"\n" +
			"With --stream the result is chant's drift record v1 NDJSON on stdout: a drift-start line, the\n" +
			"resource's drift-record line and a drift-settle line whose `reads` counts the record reads, the\n" +
			"provider Reads and the stack-wide refreshes (always 0). Needs a plunge+<url> backend.",
		Example: "plunge drift --resource 'urn:pulumi:dev::app::aws:s3/bucket:Bucket::logs' --stream",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), ws, lm, stackName, urn, member, stream, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVarP(&stackName, "stack", "s", "",
		"The name of the stack to operate on. Defaults to the current stack")
	cmd.Flags().StringVar(&urn, "resource", "", "The URN of the resource to read")
	cmd.Flags().StringVar(&member, "member", "",
		"chant's member name for the stack, written as each record's `member`. Defaults to the stack name")
	cmd.Flags().BoolVar(&stream, "stream", false, "Write chant's drift record v1 NDJSON to stdout")
	_ = cmd.MarkFlagRequired("resource")
	return cmd
}

func run(
	ctx context.Context, ws pkgWorkspace.Context, lm cmdBackend.LoginManager,
	stackName, urnArg, member string, stream bool, stdout io.Writer,
) error {
	urn := resource.URN(urnArg)
	if !urn.IsValid() {
		return fmt.Errorf("--resource %q is not a URN", urnArg)
	}

	sink := cmdutil.Diag()
	opts := display.Options{Color: cmdutil.GetGlobalColorization()}
	s, err := cmdStack.RequireStack(ctx, sink, ws, lm, stackName, cmdStack.LoadOnly, opts, "")
	if err != nil {
		return fmt.Errorf("load stack: %w", err)
	}
	if member == "" {
		member = s.Ref().Name().String()
	}

	be := s.Backend()
	host, err := backend.DefaultHostFactory(be.GetReadOnlyCloudRegistry())(ctx, sink, sink, nil)
	if err != nil {
		return fmt.Errorf("starting the plugin host: %w", err)
	}
	defer func() { _ = host.Close() }()
	pwd, err := os.Getwd()
	if err != nil {
		return err
	}
	pctx, err := plugin.NewContextWithHost(ctx, sink, sink, host, pwd, pwd, nil)
	if err != nil {
		return err
	}
	defer func() { _ = pctx.Close() }()

	command := "plunge drift --resource " + string(urn)
	if stream {
		command += " --stream"
	}
	res, err := plunge.ReadResourceDrift(ctx, be, s.Ref(), plunge.DriftRequest{
		URN:             urn,
		SecretsProvider: secrets.DefaultProvider,
		Plugins:         pctx,
		Member:          member,
		Version:         plungeVersion.Version,
		Command:         command,
	})
	// A read that listed the store still has its record: print it, then fail.
	var listed *plunge.ListedError
	if err != nil && (!errors.As(err, &listed) || res == nil) {
		return err
	}

	if stream {
		if werr := plunge.WriteDriftStream(stdout, res); werr != nil {
			return werr
		}
	} else {
		writeText(stdout, res)
	}
	return err
}

func writeText(w io.Writer, res *plunge.DriftResult) {
	r := res.Record
	fmt.Fprintf(w, "%s: %s\n", r.Address, r.Status)
	if r.Unobserved != nil {
		fmt.Fprintf(w, "  %s: %s\n", r.Unobserved.Reason, r.Unobserved.Detail)
	}
	for _, a := range r.Attributes {
		if a.Sensitive {
			fmt.Fprintf(w, "  %s: [secret]\n", a.Path)
			continue
		}
		fmt.Fprintf(w, "  %s: %v => %v\n", a.Path, a.Before, a.After)
	}
	fmt.Fprintf(w, "reads: %d record read(s) of %d key(s), %d listing(s), %d provider Read(s), %d stack refresh(es)\n",
		res.Reads.RecordsRead, res.Reads.RecordKeys, res.Reads.RecordListCalls, res.Reads.ProviderReads,
		res.Reads.StackRefreshes)
}
