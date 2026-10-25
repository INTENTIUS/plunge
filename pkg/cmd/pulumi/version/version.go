// Copyright 2016, Pulumi Corporation.
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

package version

import (
	"fmt"

	"github.com/pulumi/pulumi/pkg/v3/cmd/pulumi/constrictor"
	"github.com/pulumi/pulumi/pkg/v3/plunge"
	"github.com/pulumi/pulumi/sdk/v3/go/common/version"
	"github.com/spf13/cobra"
)

func NewVersionCmd() *cobra.Command {
	var pulumiOnly bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print plunge's version and the Pulumi version it is compatible with",
		Long: "Print plunge's version and the Pulumi version it is compatible with.\n" +
			"\n" +
			"The Pulumi-compatible version is the upstream pulumi/pulumi release plunge is based on. It is the\n" +
			"version SDKs, language hosts and plugins see in their requiredPulumiVersion checks. With\n" +
			"--pulumi-compatible, or when the binary is invoked as `pulumi` (a symlink for Automation API),\n" +
			"only that version is printed, as stock `pulumi version` does.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if pulumiOnly || plunge.InvokedAsPulumi() {
				fmt.Fprintf(cmd.OutOrStdout(), "%v\n", version.Version)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "plunge %v\npulumi %v\n", plunge.Version, version.Version)
			return nil
		},
	}
	cmd.Flags().BoolVar(&pulumiOnly, "pulumi-compatible", false,
		"Print only the Pulumi-compatible version, as a bare semver")

	constrictor.AttachArguments(cmd, constrictor.NoArgs)

	return cmd
}
