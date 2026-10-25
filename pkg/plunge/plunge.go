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

// Package plunge holds what makes this tree plunge rather than stock Pulumi: the binary's name and
// plunge's own version.
//
// plunge keeps two versions apart. Version (here) is plunge's release. The Pulumi-compatible version is
// github.com/pulumi/pulumi/sdk/v3/go/common/version.Version, left exactly where upstream reads it, so
// every check upstream makes against "the CLI version" (a project's requiredPulumiVersion, a plugin's
// pulumiVersionRange, the RequirePulumiVersion RPC that SDKs call through the engine) sees the upstream
// release plunge is based on. Both are set by the linker; see scripts/plunge-version.sh.
//
// Go module paths stay github.com/pulumi/pulumi/... so upstream merges don't touch every import.
package plunge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/common/version"
)

// BinaryName is the name of the CLI binary and the root command.
const BinaryName = "plunge"

// UpstreamUpdateCheck gates the CLI's update check. Upstream's check asks Pulumi Cloud for the latest pulumi
// release and points at pulumi's installers, which is wrong for plunge, so it is off.
const UpstreamUpdateCheck = false

// Version is plunge's own version, initialized by the Go linker:
//
//	-X github.com/pulumi/pulumi/pkg/v3/plunge.Version=v0.1.0
var Version string

// PulumiVersion is the Pulumi-compatible version this build reports to SDKs, language hosts and plugins.
func PulumiVersion() string {
	return version.Version
}

// orDev returns v, or "dev" for a build that didn't set it (a plain `go build`).
func orDev(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

// VersionString is the one-line version shown by `plunge --version`.
func VersionString() string {
	return fmt.Sprintf("%s (pulumi %s)", orDev(Version), orDev(PulumiVersion()))
}

// InvokedAsPulumi reports whether the running binary was started under the name `pulumi`, for example
// through a `pulumi -> plunge` symlink placed so that Automation API or other tooling that looks up
// `pulumi` finds plunge. Such callers parse `pulumi version` as a bare semver.
func InvokedAsPulumi() bool {
	if len(os.Args) == 0 {
		return false
	}
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0]))
	return name == "pulumi"
}
