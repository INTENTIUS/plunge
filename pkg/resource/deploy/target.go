// Copyright 2016, Pulumi Corporation.
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
	pkgresource "github.com/pulumi/pulumi/pkg/v3/resource"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/providers"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

// Target represents information about a deployment target.
type Target struct {
	Name         tokens.StackName  // the target stack name.
	Organization tokens.Name       // the target organization name (if any).
	Config       config.Map        // optional configuration key/value pairs.
	Decrypter    config.Decrypter  // decrypter for secret configuration values.
	Snapshot     *Snapshot         // the last snapshot deployed to the target.
	Tags         map[string]string // tags for the current stack.

	// ProviderIDs, when set, names the ID a newly created provider resource gets in place of a random UUID. The
	// plunge record backend sets it so that two runs creating the same default provider arrive at the same
	// URN::ID reference (INTENTIUS/plunge#6). Nil keeps upstream's behaviour.
	ProviderIDs providers.IDGenerator

	// DeleteGuard, when set, is asked about every resource the step generator would delete because the program
	// did not register it. A non-empty answer keeps the resource (as an untargeted same step) and is reported as
	// a warning naming the resource. Pending-delete states from a replacement are never asked. The plunge record
	// backend sets it so a run from an older program version cannot delete what a newer run created
	// (INTENTIUS/plunge#6). Nil keeps upstream's behaviour.
	DeleteGuard func(res *pkgresource.State) string

	// Markers, when set, injects ownership markers into every custom, non-provider resource's inputs after the
	// stack transforms run, and vets every provider's configuration (plunge_markers.go, INTENTIUS/plunge#7). Nil
	// keeps upstream's behaviour.
	Markers MarkerPolicy

	// Binder, when set, binds a planned create to the live object already carrying its markers, and refuses a
	// collision naming both IDs (plunge_markers.go, INTENTIUS/plunge#7). Nil keeps upstream's behaviour.
	Binder MarkerBinder

	// Autonames, when set, decides every custom, non-provider resource's autonaming options over the user's
	// `pulumi:autonaming` config, and may force delete-before-replace (plunge_autonaming.go, INTENTIUS/plunge#7). Nil
	// keeps upstream's behaviour.
	Autonames AutonamePolicy
}

const (
	// The package name for the NodeJS dynamic provider.
	nodejsDynamicProviderPackage = "pulumi-nodejs"
	// The package name for the Python dynamic provider.
	pythonDynamicProviderPackage = "pulumi-python"
)

// Returns true if the given package refers to a dynamic provider.
func isDynamicProvider(pkg tokens.Package) bool {
	return pkg == tokens.Package(nodejsDynamicProviderPackage) || pkg == tokens.Package(pythonDynamicProviderPackage)
}

// GetPackageConfig returns the set of configuration parameters for the indicated package, if any.
func (t *Target) GetPackageConfig(pkg tokens.Package) (property.Map, error) {
	if t == nil {
		return property.Map{}, nil
	}

	result := make(map[string]property.Value)
	for k, c := range t.Config {
		// For dynamic providers, we always pass the full configuration.
		if !isDynamicProvider(pkg) && tokens.Package(k.Namespace()) != pkg {
			continue
		}

		v, err := c.Value(t.Decrypter)
		if err != nil {
			return property.Map{}, err
		}

		propertyValue := property.New(v).WithSecret(c.Secure())
		if isDynamicProvider(pkg) {
			// For dynamic providers, we want to maintain the namespace.
			result[k.String()] = propertyValue
		} else {
			result[k.Name()] = propertyValue
		}
	}
	return property.NewMap(result), nil
}
