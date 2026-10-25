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

package deploy

import (
	"context"

	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
	"google.golang.org/protobuf/types/known/emptypb"
)

// MonitorFeatures is what the resource monitor tells a language SDK about engine features, read through the
// monitor's own RPC handlers. plunge's SDK check (pkg/plunge/sdkcheck) diffs it against what each SDK
// release asks for.
type MonitorFeatures struct {
	// Legacy holds the answer to SupportsFeature for each string id that was asked about.
	Legacy map[string]bool
	// Advertised is the feature set GetDeploymentInfo returns. Current SDKs read this and only fall back to
	// SupportsFeature when GetDeploymentInfo is unimplemented.
	Advertised []pulumirpc.ResourceMonitorFeature
}

// QueryMonitorFeatures builds a resource monitor with opts and asks it, through SupportsFeature and
// GetDeploymentInfo, about each legacy id in ids and for its advertised feature set. The monitor isn't
// served and runs no program; both handlers only read opts, so the answers are the ones a real deployment
// with the same options gives.
func QueryMonitorFeatures(ctx context.Context, opts EvalSourceOptions, ids []string) (MonitorFeatures, error) {
	rm := &resmon{opts: opts}

	legacy := make(map[string]bool, len(ids))
	for _, id := range ids {
		resp, err := rm.SupportsFeature(ctx, &pulumirpc.SupportsFeatureRequest{Id: id})
		if err != nil {
			return MonitorFeatures{}, err
		}
		legacy[id] = resp.GetHasSupport()
	}

	info, err := rm.GetDeploymentInfo(ctx, &emptypb.Empty{})
	if err != nil {
		return MonitorFeatures{}, err
	}
	return MonitorFeatures{Legacy: legacy, Advertised: info.GetSupportedFeatures()}, nil
}
