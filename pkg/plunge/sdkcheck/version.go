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

package sdkcheck

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/blang/semver"
)

var (
	// The automation API's minimum CLI version, in each SDK's spelling:
	//   go      calculateMinVersion() { semver.ParseTolerant("3.2.0-alpha") } (sdk/go/auto/minimum_version.go)
	//   nodejs  minimumVersion = new semver.SemVer("v3.2.0-alpha") (sdk/nodejs/automation/minimumVersion.ts)
	//   python  _MINIMUM_VERSION = VersionInfo.parse("3.1.0") (sdk/python/lib/pulumi/automation/_minimum_version.py)
	//   dotnet  MinimumVersion = new SemVersion(3, 1, 0), or a string literal
	minVersionString = regexp.MustCompile(
		`(?is)(?:minimum_?version|min_?version|minimumCliVersion)\w*[\s\S]{0,160}?["']v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)["']`)
	minVersionTuple = regexp.MustCompile(
		`(?is)(?:minimum_?version|min_?version)\w*[\s\S]{0,80}?SemVersion\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)`)
	// requiredPulumiVersion: a host or plugin manifest's range, when the release ships one.
	requiredRange = regexp.MustCompile(`(?i)["']?(?:requiredPulumiVersion|pulumiVersionRange)["']?\s*[:=]\s*["']([^"']+)["']`)
)

// Requirements is what an SDK release says about the CLI it needs.
type Requirements struct {
	// MinimumCLI is the automation API's minimum CLI version, if the release has one.
	MinimumCLI string
	MinimumAt  string
	// Ranges are requiredPulumiVersion / pulumiVersionRange values found in the release.
	Ranges []string
}

// FindRequirements reads the minimum CLI version and any version ranges out of an SDK release.
func FindRequirements(files []Source) Requirements {
	var r Requirements
	// The automation package's own file first, so another "minVersion" elsewhere doesn't win.
	ordered := make([]Source, 0, len(files))
	for _, f := range files {
		if strings.Contains(strings.ToLower(f.Path), "minimum") {
			ordered = append(ordered, f)
		}
	}
	for _, f := range files {
		if !strings.Contains(strings.ToLower(f.Path), "minimum") {
			ordered = append(ordered, f)
		}
	}
	for _, f := range ordered {
		if !isSource(f.Path) && !strings.HasSuffix(f.Path, ".json") && !strings.HasSuffix(f.Path, ".yaml") {
			continue
		}
		if isTestPath(f.Path) || IsGenerated(f.Data) {
			continue
		}
		if r.MinimumCLI == "" {
			if m := minVersionString.FindSubmatch(f.Data); m != nil {
				r.MinimumCLI, r.MinimumAt = string(m[1]), f.Path
			} else if m := minVersionTuple.FindSubmatch(f.Data); m != nil {
				r.MinimumCLI, r.MinimumAt = fmt.Sprintf("%s.%s.%s", m[1], m[2], m[3]), f.Path
			}
		}
		for _, m := range requiredRange.FindAllSubmatch(f.Data, -1) {
			if rng := string(m[1]); !slices.Contains(r.Ranges, rng) {
				r.Ranges = append(r.Ranges, rng)
			}
		}
	}
	return r
}

// VersionVerdicts checks an SDK release's requirements against plunge's Pulumi-compatible version, the
// version plunge reports to SDKs and Automation API. lockstep is true for the SDKs released with the CLI
// from pulumi/pulumi (nodejs, python, go): one newer than plunge's compatible version was built against an
// engine plunge hasn't merged yet, and gets an SDK-AHEAD line.
func VersionVerdicts(lang, sdkVersion, pulumiVersion string, req Requirements, lockstep bool) []Verdict {
	var out []Verdict
	plunge, err := semver.ParseTolerant(pulumiVersion)
	if err != nil {
		return []Verdict{{"VERSION-ERROR", lang, sdkVersion, "plunge-pulumi=" + pulumiVersion, err.Error()}}
	}

	switch min, err := semver.ParseTolerant(req.MinimumCLI); {
	case req.MinimumCLI == "":
		out = append(out, Verdict{
			"VERSION-UNKNOWN", lang, sdkVersion, "plunge-pulumi=" + plunge.String(),
			"no minimum CLI version found in the release",
		})
	case err != nil:
		out = append(out, Verdict{"VERSION-ERROR", lang, sdkVersion, "min-cli=" + req.MinimumCLI, err.Error()})
	case plunge.LT(min):
		out = append(out, Verdict{
			"VERSION-TOO-OLD", lang, sdkVersion,
			fmt.Sprintf("min-cli=%s plunge-pulumi=%s", min, plunge), req.MinimumAt,
		})
	default:
		out = append(out, Verdict{
			"VERSION-OK", lang, sdkVersion,
			fmt.Sprintf("min-cli=%s plunge-pulumi=%s", min, plunge), req.MinimumAt,
		})
	}

	for _, rng := range req.Ranges {
		r, err := semver.ParseRange(rng)
		switch {
		case err != nil:
			out = append(out, Verdict{"VERSION-ERROR", lang, sdkVersion, "range=" + rng, err.Error()})
		case r(plunge):
			out = append(out, Verdict{"VERSION-OK", lang, sdkVersion, "range=" + rng, "plunge-pulumi=" + plunge.String()})
		default:
			out = append(out, Verdict{
				"VERSION-TOO-OLD", lang, sdkVersion, "range=" + rng,
				"plunge-pulumi=" + plunge.String(),
			})
		}
	}

	if lockstep {
		if sdk, err := semver.ParseTolerant(sdkVersion); err == nil && sdk.GT(plunge) {
			out = append(out, Verdict{
				"SDK-AHEAD", lang, sdkVersion, "plunge-pulumi=" + plunge.String(),
				"the SDK is newer than the upstream release plunge is built from",
			})
		}
	}
	return out
}
