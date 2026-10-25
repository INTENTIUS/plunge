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

package plunge

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

func TestDeleteRefusal(t *testing.T) {
	t.Parallel()

	old := Generation{Commit: "aaa", Time: 100}
	newer := Generation{Commit: "bbb", Time: 200}

	assert.Empty(t, deleteRefusal(newer, old), "a newer run deletes what an older one wrote")
	assert.Empty(t, deleteRefusal(old, old), "equal generations delete")
	assert.Contains(t, deleteRefusal(old, newer), "bbb")
	assert.NotEmpty(t, deleteRefusal(Generation{}, old), "an unstamped run keeps stamped records")
	assert.Empty(t, deleteRefusal(Generation{}, Generation{}))
	assert.Equal(t, newer, maxGeneration(old, newer))
	assert.Equal(t, newer, maxGeneration(newer, old))
}

func TestProgramGenerationFromEnvironment(t *testing.T) {
	t.Parallel()

	env := map[string]string{EnvProgramTime: "1700000000", EnvProgramCommit: "abc"}
	g, err := ProgramGeneration(context.Background(), "", func(k string) string { return env[k] })
	require.NoError(t, err)
	assert.Equal(t, Generation{Commit: "abc", Time: 1700000000}, g)

	env[EnvProgramTime] = "yesterday"
	_, err = ProgramGeneration(context.Background(), "", func(k string) string { return env[k] })
	assert.ErrorContains(t, err, EnvProgramTime)
}

func TestDeterministicProviderID(t *testing.T) {
	t.Parallel()

	urn := resource.URN("urn:pulumi:test::test::pulumi:providers:aws::default")
	in := resource.NewPropertyMapFromMap(map[string]any{"region": "us-east-1", "version": "7.0.0"})

	a, err := DeterministicProviderID(urn, in)
	require.NoError(t, err)
	b, err := DeterministicProviderID(urn, in.Copy())
	require.NoError(t, err)
	assert.Equal(t, a, b)

	c, err := DeterministicProviderID(urn,
		resource.NewPropertyMapFromMap(map[string]any{"region": "us-west-2", "version": "7.0.0"}))
	require.NoError(t, err)
	assert.NotEqual(t, a, c, "a different configuration is a different provider")

	// A secret's value does not reach the ID.
	s1 := resource.PropertyMap{"token": resource.MakeSecret(resource.NewProperty("one"))}
	s2 := resource.PropertyMap{"token": resource.MakeSecret(resource.NewProperty("two"))}
	d1, err := DeterministicProviderID(urn, s1)
	require.NoError(t, err)
	d2, err := DeterministicProviderID(urn, s2)
	require.NoError(t, err)
	assert.Equal(t, d1, d2)
}
