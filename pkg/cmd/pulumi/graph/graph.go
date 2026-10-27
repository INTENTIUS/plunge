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

// Package graph is `plunge graph --json`, a stack's graph from its records in chant's engine graph document
// (INTENTIUS/plunge#40). Upstream's `stack graph` is a separate command and is untouched.
package graph

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/pulumi/pulumi/pkg/v3/backend/display"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge"
	cmdBackend "github.com/pulumi/pulumi/pkg/v3/cmd/pulumi/backend"
	cmdStack "github.com/pulumi/pulumi/pkg/v3/cmd/pulumi/stack"
	plungeVersion "github.com/pulumi/pulumi/pkg/v3/plunge"
	pkgWorkspace "github.com/pulumi/pulumi/pkg/v3/workspace"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/cmdutil"
)

// NewGraphCmd is `plunge graph --json [--stack <stack>]`.
func NewGraphCmd(ws pkgWorkspace.Context, lm cmdBackend.LoginManager) *cobra.Command {
	var stackName string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Print a stack's graph from its records as chant's engine graph JSON",
		Long: "Print a stack's graph from its records as chant's engine graph JSON.\n" +
			"\n" +
			"Reads the stack's records from the plunge backend in one bulk read and prints one GraphIR version 1\n" +
			"document: a node per URN with the facts the records hold (ownership, the markers the record says were\n" +
			"written, how ownership is known, cloud IDs), and `ref` edges for dependencies, parents and providers\n" +
			"(grain instance) and property dependencies (grain property). The program is not run, no provider is\n" +
			"called, no record is written, and no output value or secret is printed. Needs a plunge+<url> backend.",
		Example: "plunge graph --json --stack dev",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !jsonOut {
				return errors.New("plunge graph prints JSON only; pass --json")
			}
			return run(cmd.Context(), ws, lm, stackName, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVarP(&stackName, "stack", "s", "",
		"The name of the stack to operate on. Defaults to the current stack")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print chant's engine graph document (required)")
	return cmd
}

func run(ctx context.Context, ws pkgWorkspace.Context, lm cmdBackend.LoginManager, stackName string, stdout io.Writer) error {
	sink := cmdutil.Diag()
	opts := display.Options{Color: cmdutil.GetGlobalColorization()}
	s, err := cmdStack.RequireStack(ctx, sink, ws, lm, stackName, cmdStack.LoadOnly, opts, "")
	if err != nil {
		return err
	}
	doc, err := plunge.EmitGraph(ctx, s.Backend(), s.Ref(), plunge.GraphRequest{EngineVersion: plungeVersion.Version})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
