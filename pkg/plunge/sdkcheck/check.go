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
	"context"
	"fmt"

	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
)

// MonitorOptions are the resource monitor options of a plunge deployment on a self-managed (DIY) backend:
// the engine defaults (pkg/engine/update.go, where the CLI builds EvalSourceOptions), with state migrations
// on because the DIY backend's snapshot manager supports them (pkg/backend/diy/backend.go).
func MonitorOptions() deploy.EvalSourceOptions {
	return deploy.EvalSourceOptions{SupportsStateMigrations: true}
}

// Check runs one release against plunge's monitor: a summary line, the version verdicts, then a verdict
// per feature the release asks about.
func Check(ctx context.Context, rel Release, pulumiVersion string, opts deploy.EvalSourceOptions) ([]Verdict, error) {
	asked := Extract(rel.Files)
	monitor, err := deploy.QueryMonitorFeatures(ctx, opts, asked.Legacy)
	if err != nil {
		return nil, err
	}
	features := Diff(rel.Lang, rel.Version, asked, monitor)
	missing := 0
	for _, v := range features {
		if v.Kind == "FEATURE-MISSING" {
			missing++
		}
	}

	out := []Verdict{{
		"SDK", rel.Lang, rel.Version,
		fmt.Sprintf("files=%d legacy=%d enum=%d missing=%d", len(rel.Files), len(asked.Legacy), len(asked.Enum), missing),
		rel.Note,
	}}
	if len(asked.Legacy)+len(asked.Enum) == 0 {
		// Finding nothing means the extraction no longer matches the SDK's code, not that it asks nothing.
		out = append(out, Verdict{
			"SDK-ERROR", rel.Lang, rel.Version, "no-feature-queries-found",
			"the SDK's feature checks weren't recognised; update pkg/plunge/sdkcheck",
		})
	}
	out = append(out, VersionVerdicts(rel.Lang, rel.Version, pulumiVersion, FindRequirements(rel.Files), Lockstep(rel.Lang))...)
	return append(out, features...), nil
}
