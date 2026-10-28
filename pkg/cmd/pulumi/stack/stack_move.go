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

package stack

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
	"github.com/pulumi/pulumi/pkg/v3/cmd/pulumi/constrictor"
	"github.com/pulumi/pulumi/pkg/v3/cmd/pulumi/ui"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	pkgWorkspace "github.com/pulumi/pulumi/pkg/v3/workspace"
	"github.com/pulumi/pulumi/sdk/v3/go/common/env"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/cmdutil"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/result"
)

// newStackMoveCmd is `plunge stack move` (INTENTIUS/plunge#56): move resources to another stack of the plunge
// backend by rewriting their ownership markers and their records, behind a confirmation gate. Upstream's
// `pulumi state move` is left as it is.
func newStackMoveCmd() *cobra.Command {
	var stackName string
	var destName string
	var yes bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "move",
		Short: "Move resources to another plunge stack by rewriting their markers and records",
		Long: "Move resources to another plunge stack by rewriting their markers and records.\n" +
			"\n" +
			"Each named resource of the source stack (--stack, by default the current one) becomes the\n" +
			"destination's (--dest): its live object's plunge-stack and plunge-urn markers are rewritten through\n" +
			"its provider as a tags-only update, its record is written under the destination with the\n" +
			"destination's URN, and the source's record is removed. The provider it uses is recorded in the\n" +
			"destination too, unless the destination already holds it with the same configuration. Nothing is\n" +
			"created, replaced or deleted in the cloud, and a later delete check in either stack finds the\n" +
			"marker naming the stack that owns the object.\n" +
			"\n" +
			"The move is refused, before anything is written, when the destination already owns a resource of\n" +
			"that URN, when a live object's marker names a third stack, when rewriting a marker would replace the\n" +
			"object, when a resource that stays in the source is a child of, or depends on, a moved one, and when\n" +
			"a record has an operation in flight. A move that stops partway is finished by running it again.\n" +
			"\n" +
			"Move the resources' code to the destination's program as well; both stacks then preview no change.\n" +
			"\n" +
			"--json prints the plan or the outcome as one JSON document and never prompts. Alone it writes\n" +
			"nothing; with --yes it moves and prints the outcome. A refused move prints the document with its\n" +
			"refusals and exits non-zero.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			yes = yes || env.SkipConfirmations.Value()
			opts := display.Options{Color: cmdutil.GetGlobalColorization()}
			if destName == "" {
				return errors.New("--dest names the stack to move the resources to")
			}
			urns := make([]resource.URN, 0, len(args))
			for _, a := range args {
				urn, err := resource.ParseURN(a)
				if err != nil {
					return fmt.Errorf("%q is not a URN: %w", a, err)
				}
				urns = append(urns, urn)
			}

			sink := cmdutil.Diag()
			s, err := RequireStack(ctx, sink, pkgWorkspace.Instance, cmdBackend.DefaultLoginManager, stackName,
				LoadOnly, opts, "")
			if err != nil {
				return err
			}
			mover, ok := s.Backend().(plunge.Mover)
			if !ok {
				return fmt.Errorf("`stack move` needs the plunge backend; %s is %s. Use `pulumi state move` on "+
					"other backends", s.Ref(), s.Backend().Name())
			}
			destRef, err := s.Backend().ParseStackReference(destName)
			if err != nil {
				return err
			}
			dest, err := s.Backend().GetStack(ctx, destRef)
			if err != nil {
				return err
			}
			if dest == nil {
				return fmt.Errorf("the destination stack %s does not exist; create it with `plunge stack init` "+
					"and run `plunge up` on it once", destRef)
			}

			host, err := backend.DefaultHostFactory(s.Backend().GetReadOnlyCloudRegistry())(ctx, sink, sink, nil)
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

			moveOpts := plunge.MoveOptions{URNs: urns, SecretsProvider: secrets.DefaultProvider, Plugins: pctx}
			if jsonOut {
				return runStackMoveJSON(ctx, cmd.OutOrStdout(), mover, s, dest, moveOpts, yes)
			}

			// The gate: check and plan without writing, show it, and confirm.
			res, err := mover.Move(ctx, s, dest, moveOpts)
			if res != nil {
				printMove(cmd.OutOrStdout(), res)
			}
			if err != nil {
				return err
			}
			pending := 0
			for _, r := range res.Resources {
				if r.Status != plunge.MoveAlreadyMoved {
					pending++
				}
			}
			if pending == 0 {
				return nil
			}
			prompt := fmt.Sprintf("This moves %d resource(s) from %s to %s, rewriting the live markers listed above "+
				"and the records of both stacks.", pending, s.Ref(), dest.Ref())
			if err := ui.ConfirmDeletion(yes, cmdutil.Interactive(), prompt, dest.Ref().String(),
				cmd.OutOrStdout(), opts); err != nil {
				return err
			}

			moveOpts.Confirmed = true
			res, err = mover.Move(ctx, s, dest, moveOpts)
			if res != nil {
				printMove(cmd.OutOrStdout(), res)
			}
			return err
		},
	}

	constrictor.AttachArguments(cmd, &constrictor.Arguments{
		Arguments: []constrictor.Argument{{Name: "urn"}},
		Required:  1,
		Variadic:  true,
	})

	cmd.PersistentFlags().StringVarP(
		&stackName, "stack", "s", "", "The source stack. Defaults to the current stack")
	cmd.PersistentFlags().StringVar(
		&destName, "dest", "", "The stack to move the resources to")
	cmd.PersistentFlags().BoolVarP(
		&yes, "yes", "y", false, "Skip the confirmation prompt and move")
	cmd.PersistentFlags().BoolVarP(
		&jsonOut, "json", "j", false, "Print the plan or outcome as JSON; without --yes nothing is written")
	return cmd
}

// moveView is `plunge stack move --json`'s document.
type moveView struct {
	Status   string               `json:"status"` // "planned", "moved", "refused" or "interrupted"
	Result   *plunge.MoveResult   `json:"result,omitempty"`
	Refusals []plunge.MoveRefusal `json:"refusals,omitempty"`
	Error    string               `json:"error,omitempty"`
}

func runStackMoveJSON(
	ctx context.Context, w io.Writer, mover plunge.Mover, src, dest backend.Stack, opts plunge.MoveOptions, yes bool,
) error {
	opts.Confirmed = yes
	res, err := mover.Move(ctx, src, dest, opts)
	view := moveView{Result: res, Status: "planned"}
	if yes {
		view.Status = "moved"
	}
	var refused *plunge.MoveRefusedError
	switch {
	case errors.As(err, &refused):
		view.Status, view.Refusals = "refused", refused.Refusals
	case err != nil && res != nil:
		view.Status, view.Error = "interrupted", err.Error()
	case err != nil:
		return err
	}
	if perr := ui.FprintJSON(w, view); perr != nil {
		return perr
	}
	if err != nil {
		return result.BailError(err)
	}
	return nil
}

func printMove(w io.Writer, res *plunge.MoveResult) {
	verb := "Moving"
	if res.Written {
		verb = "Moved"
	}
	fmt.Fprintf(w, "%s from %s to %s:\n", verb, res.Source, res.Destination)
	for _, r := range res.Resources {
		line := fmt.Sprintf("  %s -> %s", r.URN, r.DestinationURN)
		if r.ID != "" {
			line += "  " + string(r.ID)
		}
		fmt.Fprintf(w, "%s  [%s, ownership: %s]\n", line, r.Status, r.Ownership)
		switch {
		case r.Ownership == plunge.OwnershipMarker && r.MarkerWrite:
			fmt.Fprintf(w, "    markers rewritten by a tags-only update: %s\n", formatTags(r.Markers))
		case r.Ownership == plunge.OwnershipMarker:
			fmt.Fprintf(w, "    markers recorded as %s; the provider reports no change to make\n", formatTags(r.Markers))
		}
		for _, d := range r.Dropped {
			fmt.Fprintf(w, "    dependency on %s stays in the source and is dropped\n", d)
		}
	}
	for _, p := range res.Providers {
		how := "recorded in the destination"
		if p.Reused {
			how = "already in the destination with the same configuration"
		}
		fmt.Fprintf(w, "  provider %s -> %s (ID %s): %s\n", p.URN, p.DestinationURN, p.ID, how)
	}
}
