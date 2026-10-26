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

package version

// `plunge version` (INTENTIUS/plunge#3). These tests set the linker-stamped package variables and os.Args, so
// none of them runs in parallel.

import (
	"bytes"
	"os"
	"regexp"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/plunge"
	"github.com/pulumi/pulumi/sdk/v3/go/common/version"
)

// stamp sets plunge's and the Pulumi-compatible version, and the binary name the process was invoked as, for the
// rest of the test.
func stamp(t *testing.T, plungeVersion, pulumiVersion, argv0 string) {
	t.Helper()
	oldPlunge, oldPulumi, oldArgs := plunge.Version, version.Version, os.Args
	t.Cleanup(func() { plunge.Version, version.Version, os.Args = oldPlunge, oldPulumi, oldArgs })
	plunge.Version, version.Version = plungeVersion, pulumiVersion
	os.Args = append([]string{argv0}, oldArgs[1:]...)
}

func runVersion(t *testing.T, args ...string) string {
	t.Helper()
	cmd := NewVersionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	require.NoError(t, cmd.Execute())
	return out.String()
}

// A bare semver, with the leading v stock `pulumi version` prints, and nothing else on the line.
var bareSemver = regexp.MustCompile(`^v(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)\n$`)

func assertBareSemver(t *testing.T, out, want string) {
	t.Helper()
	assert.Equal(t, want+"\n", out)
	m := bareSemver.FindStringSubmatch(out)
	require.NotNil(t, m, "%q is not a bare semver line", out)
	_, err := semver.Parse(m[1])
	assert.NoError(t, err, "Automation API parses this with a semver parser")
}

// `plunge version` prints plunge's version and the Pulumi-compatible one, on two lines.
//
//nolint:paralleltest // sets package variables and os.Args
func TestVersionPrintsBothVersions(t *testing.T) {
	stamp(t, "v0.1.0-dev.0", "v3.272.0", "/usr/local/bin/plunge")
	assert.Equal(t, "plunge v0.1.0-dev.0\npulumi v3.272.0\n", runVersion(t))
}

// --pulumi-compatible prints only the Pulumi-compatible version, as a bare semver.
//
//nolint:paralleltest // sets package variables and os.Args
func TestVersionPulumiCompatibleIsBareSemver(t *testing.T) {
	stamp(t, "v0.1.0-dev.0", "v3.272.0", "/usr/local/bin/plunge")
	assertBareSemver(t, runVersion(t, "--pulumi-compatible"), "v3.272.0")

	// A prerelease compatible version stays one bare semver.
	stamp(t, "v0.1.0-dev.0", "v3.273.0-alpha.1+abc123", "/usr/local/bin/plunge")
	assertBareSemver(t, runVersion(t, "--pulumi-compatible"), "v3.273.0-alpha.1+abc123")
}

// Through a symlink named pulumi (on Windows, pulumi.exe), `version` prints only the bare semver, with or without
// the flag, so Automation API's version check reads plunge as the Pulumi release it is based on.
//
//nolint:paralleltest // sets package variables and os.Args
func TestVersionThroughAPulumiSymlinkIsBareSemver(t *testing.T) {
	for _, argv0 := range []string{"/usr/local/bin/pulumi", "pulumi", `C:\tools\pulumi.exe`} {
		stamp(t, "v0.1.0-dev.0", "v3.272.0", argv0)
		if argv0 == `C:\tools\pulumi.exe` && os.PathSeparator != '\\' {
			// filepath.Base only splits on the platform's separator.
			continue
		}
		require.True(t, plunge.InvokedAsPulumi(), argv0)
		assertBareSemver(t, runVersion(t), "v3.272.0")
		assertBareSemver(t, runVersion(t, "--pulumi-compatible"), "v3.272.0")
	}

	// A binary named anything else is plunge.
	for _, argv0 := range []string{"/usr/local/bin/plunge", "pulumi-language-go", "pulumix"} {
		stamp(t, "v0.1.0-dev.0", "v3.272.0", argv0)
		assert.False(t, plunge.InvokedAsPulumi(), argv0)
		assert.Equal(t, "plunge v0.1.0-dev.0\npulumi v3.272.0\n", runVersion(t))
	}
}
