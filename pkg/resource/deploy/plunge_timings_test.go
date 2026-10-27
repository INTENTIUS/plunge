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


package deploy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlungeTimingsTakeOnce(t *testing.T) {
	t.Parallel()

	urn := resource.URN("urn:pulumi:dev::proj::aws:s3/bucket:Bucket::b")
	timings := NewTimings()
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	timings.Record(urn, apitype.PlungeTimingProvider, "create", start, 1500*time.Microsecond, false)
	timings.Record(urn, apitype.PlungeTimingRecord, "write", start, 2*time.Millisecond, true)

	got := timings.Take(urn)
	require.Len(t, got, 2)
	assert.Equal(t, apitype.PlungeTiming{
		Kind: "provider", Call: "create", StartedAt: "2026-10-27T12:00:00Z", DurationMs: 1.5,
	}, got[0])
	assert.Equal(t, "write", got[1].Call)
	assert.True(t, got[1].Failed)
	assert.Empty(t, timings.Take(urn), "each call is carried by one event")

	var none *Timings
	none.Record(urn, apitype.PlungeTimingProvider, "create", start, time.Millisecond, false)
	assert.Nil(t, none.Take(urn))
}

func TestPlungeTimingsAbsentFromStockEvents(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(apitype.StepEventMetadata{Op: apitype.OpCreate})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "plungeTimings")
}
