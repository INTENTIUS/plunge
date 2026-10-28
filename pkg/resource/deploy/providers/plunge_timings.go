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

package providers

import "github.com/pulumi/pulumi/pkg/v3/resource/plugin"

// SetProviderWrapper makes GetProvider hand out wrap(provider) in place of each registered provider. A nil wrap
// restores the providers as registered. plunge's record backend uses it to time each resource's provider calls
// (INTENTIUS/plunge#55).
func (r *Registry) SetProviderWrapper(wrap func(plugin.Provider) plugin.Provider) {
	r.m.Lock()
	defer r.m.Unlock()
	r.wrap = wrap
}
