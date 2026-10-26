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

package gcplist

// The types with no Asset name in choudoufu's pkg/identity, written out (INTENTIUS/plunge#19, paired with
// INTENTIUS/choudoufu#2274): Cloud SQL instances and GKE clusters, whose rows are handwritten in pkg/identity
// without an Asset template, and google_assured_workloads_workload, whose list is under the organization its own
// required input names. Each list reads the service's own store; none of the three services publishes a list
// consistency model, so each binds and its consistency is unmeasured (the ruling on INTENTIUS/plunge#15). The same
// three lines are in testdata/covered-types.txt in both repos.

import "strings"

var (
	svcCloudSQL = Service{Host: "sqladmin.googleapis.com", Root: "https://sqladmin.googleapis.com/sql/v1beta4/",
		EndpointArg: "sqlCustomEndpoint", EndpointEnv: "GOOGLE_SQL_CUSTOM_ENDPOINT", Items: "items"}
	svcContainer = Service{Host: "container.googleapis.com", Root: "https://container.googleapis.com/v1/",
		EndpointArg: "containerCustomEndpoint", EndpointEnv: "GOOGLE_CONTAINER_CUSTOM_ENDPOINT", Items: "clusters"}
	svcAssuredWorkloads = Service{Host: "assuredworkloads.googleapis.com",
		Root:        "https://{{location}}-assuredworkloads.googleapis.com/v1/",
		EndpointArg: "assuredWorkloadsCustomEndpoint", EndpointEnv: "GOOGLE_ASSURED_WORKLOADS_CUSTOM_ENDPOINT",
		Items: "workloads"}
)

// Handwritten are the written-out types with no Asset name, by Terraform type. Each lists at the collection its
// Terraform id is built under, and names each object by that id.
var Handwritten = map[string]Type{
	// The Cloud SQL Admin API names an instance bare; its selfLink carries projects/<p>/instances/<name>, the
	// instance's Terraform id. Labels are the instance settings' userLabels.
	"google_sql_database_instance": {Service: svcCloudSQL, Collection: "projects/{{project}}/instances",
		LabelsAt: []string{"settings", "userLabels"}},
	// GKE lists every location's clusters at locations/-, so the list does not depend on which of the provider's
	// region or zone the cluster's location defaulted to. Each cluster is named by its Terraform id,
	// projects/<p>/locations/<location>/clusters/<name>, from its own location field. Labels are resourceLabels.
	"google_container_cluster": {Service: svcContainer, Collection: "projects/{{project}}/locations/-/clusters",
		LabelsAt: []string{"resourceLabels"}, ItemName: gkeClusterName},
	// A workload's name is its full relative name, organizations/<o>/locations/<l>/workloads/<id>, which is its
	// Terraform id. organization and location are the resource's required inputs.
	"google_assured_workloads_workload": {Service: svcAssuredWorkloads,
		Collection: "organizations/{{organization}}/locations/{{location}}/workloads"},
}

func gkeClusterName(o map[string]any, coll string) string {
	name, _ := o["name"].(string)
	loc, _ := o["location"].(string)
	if name == "" || loc == "" {
		return ""
	}
	project := strings.TrimSuffix(coll, "/locations/-/clusters")
	return project + "/locations/" + loc + "/clusters/" + name
}

// handwritten returns tfType's written-out list, if it is one of [Handwritten].
func handwritten(tfType string) (Type, bool) {
	t, ok := Handwritten[tfType]
	if !ok {
		return Type{}, false
	}
	t.TFType = tfType
	return t, true
}
