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

package plugin

import "context"

// AutonamingSupporter is a provider that can say whether it honours the autonaming options Check is given. plunge's
// autonaming policy reads it (INTENTIUS/plunge#10): a provider that ignores a proposed name picks its own, so the
// resource's record must not claim the deterministic name.
type AutonamingSupporter interface {
	// SupportsAutonamingConfiguration waits for the provider to be configured, as Check does, and reports whether
	// it accepts autonaming configuration. known is false when the configuration is not fully known (a preview with
	// unknown provider inputs), in which case Check is not sent to the provider either.
	SupportsAutonamingConfiguration(ctx context.Context) (supported, known bool, err error)
}

var _ AutonamingSupporter = (*provider)(nil)

// SupportsAutonamingConfiguration implements AutonamingSupporter.
func (p *provider) SupportsAutonamingConfiguration(ctx context.Context) (bool, bool, error) {
	protocol, pcfg, err := p.getPluginConfig(ctx)
	if err != nil {
		return false, false, err
	}
	if !pcfg.known {
		return false, false, nil
	}
	return protocol.supportsAutonamingConfiguration, true, nil
}
