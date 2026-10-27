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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/intentius/choudoufu/pkg/adoption"
	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/pkg/v3/backend/display"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge"
	cmdBackend "github.com/pulumi/pulumi/pkg/v3/cmd/pulumi/backend"
	"github.com/pulumi/pulumi/pkg/v3/cmd/pulumi/constrictor"
	"github.com/pulumi/pulumi/pkg/v3/cmd/pulumi/ui"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	pkgWorkspace "github.com/pulumi/pulumi/pkg/v3/workspace"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/env"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/cmdutil"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/result"

	"github.com/pulumi/pulumi/pkg/v3/backend/secrets"
)

// newStackAdoptCmd is `plunge stack adopt` (INTENTIUS/plunge#7): adopt a stock Pulumi stack into the plunge record
// backend, behind a confirmation gate.
func newStackAdoptCmd() *cobra.Command {
	var stackName string
	var file string
	var yes bool
	var jsonOut bool
	var adoptFile string
	var indexStore string

	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "Adopt a stock Pulumi stack into plunge's per-resource records",
		Long: "Adopt a stock Pulumi stack into plunge's per-resource records.\n" +
			"\n" +
			"Run against a plunge backend (plunge+<diy url>). By default the stock checkpoint is the one the DIY\n" +
			"backend at the same root holds for the stack; --file adopts an exported deployment instead.\n" +
			"\n" +
			"Every resource's state becomes its own record with the checkpoint's IDs, unchanged. Nothing is read\n" +
			"from or written to the cloud. The next `plunge up` adds plunge's ownership tags (plunge-stack,\n" +
			"plunge-urn) to each taggable resource in place; that is the only change adoption brings.\n" +
			"\n" +
			"Adoption is refused, before anything is written, when the checkpoint has pending operations, when a\n" +
			"provider's ignoreTags or defaultTags would hide the ownership tags, and when a resource's tags can only\n" +
			"be set at create (marking it would replace it). Adopting the same checkpoint again does nothing.\n" +
			"\n" +
			"To back out: `plunge stack export --file s.json`, then `pulumi stack import --file s.json` on a stock\n" +
			"backend. `pulumi preview --refresh` then shows only the removal of the plunge-* tags.\n" +
			"\n" +
			"--json prints the classification as one JSON document (docs/plunge/ADOPT.md) and never prompts. Alone it\n" +
			"writes nothing; with --yes it adopts and prints the result. A refused adoption prints the document with\n" +
			"its refusals and exits non-zero.\n" +
			"\n" +
			"--adopt FILE adopts indexed cloud objects instead: an `adopt: v1` adoption file (chant's schema) names\n" +
			"an index version in the index store (--index-store, by default behold's: the parent of\n" +
			"$BEHOLD_INDEX_DIR, else $XDG_DATA_HOME/behold or ~/.local/share/behold) and the nodes to adopt, each\n" +
			"with its resource name (`as`) and, where the inventory type does not join to one, its Pulumi type.\n" +
			"Each object is read again through the stack's own provider, configured from the stack's\n" +
			"configuration, and that read decides: an object carrying another stack's marker, a type whose tags\n" +
			"can only be set at create, a type that cannot be imported and an object the read does not find are\n" +
			"refused, before anything is written. Run it in the stack's project directory. Adoption writes the\n" +
			"records and no marker; the next `plunge up` writes each object's markers as a tags-only update,\n" +
			"which the adoption lists and up's preview and confirmation show.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ws := pkgWorkspace.Instance
			yes = yes || env.SkipConfirmations.Value()
			opts := display.Options{Color: cmdutil.GetGlobalColorization()}

			s, err := RequireStack(ctx, cmdutil.Diag(), ws, cmdBackend.DefaultLoginManager, stackName, LoadOnly, opts, "")
			if err != nil {
				return err
			}
			adopter, ok := s.Backend().(plunge.Adopter)
			if !ok {
				return fmt.Errorf("`stack adopt` needs the plunge backend; %s is %s. Log in with `plunge login "+
					"plunge+<diy url>` at the same root as the stock stack", s.Ref(), s.Backend().Name())
			}

			adoptOpts := plunge.AdoptOptions{SecretsProvider: secrets.DefaultProvider}
			if adoptFile != "" {
				if file != "" {
					return errors.New("--adopt and --file each name what to adopt; pass one")
				}
				ix, closeIndex, err := indexAdoptOptions(ctx, ws, s, adoptFile, indexStore)
				if err != nil {
					return err
				}
				defer closeIndex()
				adoptOpts.Index = ix
			} else if file != "" {
				d, err := readDeploymentFile(file)
				if err != nil {
					return err
				}
				adoptOpts.Checkpoint, adoptOpts.Source = d, "the file "+file
			}

			if jsonOut {
				return runStackAdoptJSON(ctx, cmd.OutOrStdout(), adopter, s, adoptOpts, yes)
			}

			// The gate: classify without writing, show it, and confirm.
			res, err := adopter.Adopt(ctx, s, adoptOpts)
			var unconfirmed *plunge.AdoptUnconfirmedError
			switch {
			case err == nil:
				printAdoption(cmd.OutOrStdout(), s.Ref().String(), res)
				return nil
			case !errors.As(err, &unconfirmed):
				return err
			}
			printAdoption(cmd.OutOrStdout(), s.Ref().String(), unconfirmed.Result)
			prompt := fmt.Sprintf("This writes plunge records for %d resources of %s.",
				len(unconfirmed.Result.Resources), s.Ref())
			if adoptOpts.Index != nil {
				prompt += " Nothing is written to the cloud now: the next `plunge up` writes the markers listed " +
					"above, tags only, after its own preview and confirmation."
			}
			if err := ui.ConfirmDeletion(yes, cmdutil.Interactive(), prompt,
				s.Ref().String(), cmd.OutOrStdout(), opts); err != nil {
				return err
			}

			adoptOpts.Confirmed = true
			res, err = adopter.Adopt(ctx, s, adoptOpts)
			if err != nil {
				return err
			}
			printAdoption(cmd.OutOrStdout(), s.Ref().String(), res)
			return nil
		},
	}

	constrictor.AttachArguments(cmd, constrictor.NoArgs)

	cmd.PersistentFlags().StringVarP(
		&stackName, "stack", "s", "", "The name of the stack to operate on. Defaults to the current stack")
	cmd.PersistentFlags().StringVarP(
		&file, "file", "", "", "Adopt this exported deployment instead of the DIY checkpoint at the same root")
	cmd.PersistentFlags().BoolVarP(
		&yes, "yes", "y", false, "Skip the confirmation prompt and adopt")
	cmd.PersistentFlags().BoolVarP(
		&jsonOut, "json", "j", false, "Print the classification as JSON; without --yes nothing is written")
	cmd.PersistentFlags().StringVar(
		&adoptFile, "adopt", "", "Adopt the indexed objects this adoption file (adopt: v1) selects")
	cmd.PersistentFlags().StringVar(
		&indexStore, "index-store", "", "The index store holding the adoption file's index version (the directory "+
			"with index/ in it); defaults to behold's per-user store")
	return cmd
}

// runStackAdoptJSON is `plunge stack adopt --json` (INTENTIUS/plunge#17). Without yes the adoption is not
// confirmed, so Adopt classifies and writes nothing. A refusal is the document and a bail, so the exit is non-zero
// and the refusal is not printed a second time as text.
func runStackAdoptJSON(
	ctx context.Context, w io.Writer, adopter plunge.Adopter, s backend.Stack, opts plunge.AdoptOptions, yes bool,
) error {
	opts.Confirmed = yes
	res, err := adopter.Adopt(ctx, s, opts)
	view, verr := plunge.NewAdoptView(s.Ref().String(), res, err)
	if verr != nil {
		return verr
	}
	if perr := ui.FprintJSON(w, view); perr != nil {
		return perr
	}
	if view.Status == plunge.AdoptRefused {
		return result.BailError(err)
	}
	return nil
}

// defaultIndexStore is behold's per-user index store, the directory holding index/ (behold
// src/graph-db/sources.ts userIndexDir): the parent of $BEHOLD_INDEX_DIR, else $XDG_DATA_HOME/behold, else
// ~/.local/share/behold.
func defaultIndexStore() (string, error) {
	if dir := os.Getenv("BEHOLD_INDEX_DIR"); dir != "" {
		return filepath.Dir(filepath.Clean(dir)), nil
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "behold"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding behold's index store: %w (pass --index-store)", err)
	}
	return filepath.Join(home, ".local", "share", "behold"), nil
}

// indexAdoptOptions reads the adoption file against its index version and sets up the stack's providers and
// secrets manager for `plunge stack adopt --adopt` (INTENTIUS/plunge#52). The providers are configured from the
// project's stack configuration, as `plunge up` configures the default providers.
func indexAdoptOptions(
	ctx context.Context, ws pkgWorkspace.Context, s backend.Stack, adoptFile, indexStore string,
) (*plunge.IndexAdoptOptions, func(), error) {
	if indexStore == "" {
		var err error
		if indexStore, err = defaultIndexStore(); err != nil {
			return nil, nil, err
		}
	}
	a, err := adoption.Open(adoptFile, indexStore)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the adoption file: %w", err)
	}

	sink := cmdutil.Diag()
	pwd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	project, _, err := ws.ReadProject(pwd)
	if err != nil {
		return nil, nil, fmt.Errorf("--adopt reads the stack's provider configuration from its project: %w", err)
	}
	ps, err := LoadProjectStack(ctx, sink, project, s, "")
	if err != nil {
		return nil, nil, fmt.Errorf("loading the configuration of %s: %w", s.Ref(), err)
	}
	ssml := NewStackSecretsManagerLoaderFromEnv()
	sm, state, err := ssml.GetSecretsManager(ctx, s, ps)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the secrets manager of %s: %w", s.Ref(), err)
	}
	if state != SecretsManagerUnchanged {
		if err := SaveProjectStack(ctx, s, ps, ""); err != nil {
			return nil, nil, fmt.Errorf("saving the configuration of %s: %w", s.Ref(), err)
		}
	}

	host, err := backend.DefaultHostFactory(s.Backend().GetReadOnlyCloudRegistry())(ctx, sink, sink, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("starting the plugin host: %w", err)
	}
	pctx, err := plugin.NewContextWithHost(ctx, sink, sink, host, pwd, pwd, nil)
	if err != nil {
		_ = host.Close()
		return nil, nil, err
	}
	provs, err := plunge.NewStackProviders(pctx, s.Ref(), ps.Config, sm.Decrypter())
	if err != nil {
		_ = pctx.Close()
		_ = host.Close()
		return nil, nil, err
	}
	closeAll := func() {
		_ = provs.Close()
		_ = pctx.Close()
		_ = host.Close()
	}
	return &plunge.IndexAdoptOptions{Adoption: a, File: adoptFile, Providers: provs, SecretsManager: sm}, closeAll, nil
}

func readDeploymentFile(path string) (*apitype.UntypedDeployment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open file: %w", err)
	}
	defer contract.IgnoreClose(f)
	var d apitype.UntypedDeployment
	if err := json.NewDecoder(f).Decode(&d); err != nil {
		return nil, fmt.Errorf("could not read deployment: %w", err)
	}
	return &d, nil
}

func printAdoption(w io.Writer, stack string, res *plunge.AdoptResult) {
	switch {
	case res.AlreadyAdopted:
		fmt.Fprintf(w, "%s was already adopted from this checkpoint (digest %s); nothing was written.\n",
			stack, res.Digest)
		return
	case res.Written:
		fmt.Fprintf(w, "Adopted %s: %d resources recorded (checkpoint digest %s).\n", stack, len(res.Resources),
			res.Digest)
	default:
		fmt.Fprintf(w, "Adopting %s (from %s, digest %s):\n", stack, res.Source, res.Digest)
	}
	for _, r := range res.Resources {
		line := fmt.Sprintf("  %s  %s  ownership: %s", r.URN, r.ID, r.Ownership)
		if r.Node != "" {
			line += "  node: " + r.Node
		}
		if r.Reason != "" {
			line += " (" + r.Reason + ")"
		}
		fmt.Fprintln(w, line)
		switch {
		case r.MarkerWrite != nil:
			fmt.Fprintf(w, "    marker write at the next `plunge up`, tags only: %s += %s\n", r.MarkerWrite.Property,
				formatTags(r.MarkerWrite.Tags))
		case r.Node != "" && r.Marked:
			fmt.Fprintln(w, "    already carries its markers; no marker write")
		}
	}
}

// formatTags is a marker set as key=value pairs, sorted by key.
func formatTags(tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += k + "=" + tags[k]
	}
	return "{" + out + "}"
}
