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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An SDK that asks for a feature plunge lacks gets a FEATURE-MISSING line naming it, for a legacy
// SupportsFeature id and for an enum value plunge's proto doesn't have.
func TestCheckReportsMissingFeatureByName(t *testing.T) {
	t.Parallel()

	rel := Release{Lang: "nodejs", Version: "3.999.0", Files: []Source{
		{Path: "package/runtime/settings.js", Data: []byte(`
const legacyFeatureMapping = {
    secrets: resproto.ResourceMonitorFeature.RESOURCE_MONITOR_FEATURE_SECRETS,
};
store.supportsSecrets = features.includes(resproto.ResourceMonitorFeature.RESOURCE_MONITOR_FEATURE_SECRETS);
store.supportsFuture = features.includes(resproto.ResourceMonitorFeature.RESOURCE_MONITOR_FEATURE_PLUNGE_FUTURE);
await monitorSupportsFeature(monitorRef, "plungeFuture");
`)},
		{Path: "package/automation/minimumVersion.js", Data: []byte(`exports.minimumVersion = new semver.SemVer("v3.2.0-alpha");`)},
		// Generated code lists every enum value; it must not count as asking.
		{Path: "package/proto/resource_pb.js", Data: []byte(`// GENERATED CODE -- DO NOT EDIT!
RESOURCE_MONITOR_FEATURE_OTHER_GENERATED: 99`)},
	}}

	vs, err := Check(context.Background(), rel, "3.272.0", MonitorOptions())
	require.NoError(t, err)

	var lines []string
	for _, v := range vs {
		lines = append(lines, v.String())
	}
	text := strings.Join(lines, "\n")

	assert.Contains(t, text, "FEATURE-MISSING nodejs 3.999.0 plungeFuture (SupportsFeature answers false)")
	assert.Contains(t, text, "FEATURE-MISSING nodejs 3.999.0 RESOURCE_MONITOR_FEATURE_PLUNGE_FUTURE")
	assert.Contains(t, text, "FEATURE-OK nodejs 3.999.0 secrets (SupportsFeature)")
	assert.Contains(t, text, "FEATURE-OK nodejs 3.999.0 RESOURCE_MONITOR_FEATURE_SECRETS (GetDeploymentInfo)")
	assert.Contains(t, text, "VERSION-OK nodejs 3.999.0 min-cli=3.2.0-alpha plunge-pulumi=3.272.0")
	assert.Contains(t, text, "SDK-AHEAD nodejs 3.999.0 plunge-pulumi=3.272.0")
	assert.NotContains(t, text, "OTHER_GENERATED")
}

func TestScreamingSnake(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "RESOURCE_REFERENCES", screamingSnake("ResourceReferences"))
	assert.Equal(t, "SECRETS", screamingSnake("Secrets"))
	assert.Equal(t, "INVOKE_DEPENDS_ON", screamingSnake("InvokeDependsOn"))
}

func TestCSharpEnumAndLegacy(t *testing.T) {
	t.Parallel()

	asked := Extract([]Source{{Path: "sdk/Pulumi/Deployment/Deployment.cs", Data: []byte(`
var supports = await MonitorSupportsFeature("deletedWith").ConfigureAwait(false);
if (features.Contains(ResourceMonitorFeature.ResourceReferences)) {}
`)}})
	assert.Equal(t, []string{"deletedWith"}, asked.Legacy)
	assert.Equal(t, []string{"RESOURCE_MONITOR_FEATURE_RESOURCE_REFERENCES"}, asked.Enum)
}
