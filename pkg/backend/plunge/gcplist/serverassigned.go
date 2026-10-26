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

// The server-assigned types, ported from choudoufu's internal/live/gcpsweep/typelist.go (INTENTIUS/choudoufu#2264):
// ten labelled types whose identity the API assigns at create time, so two concurrent creates both succeed, and whose
// list is scoped by a project and a location. Their services are not in [Services], so the rule in gcplist.go does
// not derive them; each is written out here with the collection the provider's create call posts to. With them,
// plunge and choudoufu list the same set of types (testdata/covered-types.txt, which both repos test against).
//
// A root may carry {{region}} or {{location}}: Vertex AI and Document AI are served at a regional host. [Type.URL]
// fills it with the resource's own location, as the provider does. A custom endpoint replaces the root whole.

var (
	svcVertexAI = Service{Host: "aiplatform.googleapis.com", Root: "https://{{region}}-aiplatform.googleapis.com/v1/",
		EndpointArg: "vertexAiCustomEndpoint", EndpointEnv: "GOOGLE_VERTEX_AI_CUSTOM_ENDPOINT"}
	svcDocumentAI = Service{Host: "documentai.googleapis.com", Root: "https://{{location}}-documentai.googleapis.com/v1/",
		EndpointArg: "documentAiCustomEndpoint", EndpointEnv: "GOOGLE_DOCUMENT_AI_CUSTOM_ENDPOINT"}
	svcTranscoder = Service{Host: "transcoder.googleapis.com", Root: "https://transcoder.googleapis.com/v1/",
		EndpointArg: "transcoderCustomEndpoint", EndpointEnv: "GOOGLE_TRANSCODER_CUSTOM_ENDPOINT"}
	svcServiceDirectory = Service{Host: "servicedirectory.googleapis.com",
		Root:        "https://servicedirectory.googleapis.com/v1/",
		EndpointArg: "serviceDirectoryCustomEndpoint", EndpointEnv: "GOOGLE_SERVICE_DIRECTORY_CUSTOM_ENDPOINT"}
	svcRecaptcha = Service{Host: "recaptchaenterprise.googleapis.com",
		Root:        "https://recaptchaenterprise.googleapis.com/v1/",
		EndpointArg: "recaptchaEnterpriseCustomEndpoint", EndpointEnv: "GOOGLE_RECAPTCHA_ENTERPRISE_CUSTOM_ENDPOINT"}
	svcManagedAD = Service{Host: "managedidentities.googleapis.com", Root: "https://managedidentities.googleapis.com/v1/",
		EndpointArg: "activeDirectoryCustomEndpoint", EndpointEnv: "GOOGLE_ACTIVE_DIRECTORY_CUSTOM_ENDPOINT"}
)

// ServerAssigned are the server-assigned types, by Terraform type. Vertex AI's location is the resource's region
// argument (pulumi-gcp and hashicorp/google default it to the provider's region); the others read location.
var ServerAssigned = map[string]Type{
	"google_vertex_ai_dataset": {Service: svcVertexAI,
		Collection: "projects/{{project}}/locations/{{region}}/datasets"},
	"google_vertex_ai_index": {Service: svcVertexAI,
		Collection: "projects/{{project}}/locations/{{region}}/indexes"},
	"google_vertex_ai_index_endpoint": {Service: svcVertexAI,
		Collection: "projects/{{project}}/locations/{{region}}/indexEndpoints"},
	"google_vertex_ai_reasoning_engine": {Service: svcVertexAI,
		Collection: "projects/{{project}}/locations/{{region}}/reasoningEngines"},
	"google_vertex_ai_tensorboard": {Service: svcVertexAI,
		Collection: "projects/{{project}}/locations/{{region}}/tensorboards"},
	"google_document_ai_schema": {Service: svcDocumentAI,
		Collection: "projects/{{project}}/locations/{{location}}/schemas"},
	"google_transcoder_job": {Service: svcTranscoder,
		Collection: "projects/{{project}}/locations/{{location}}/jobs"},
	"google_service_directory_namespace": {Service: svcServiceDirectory,
		Collection: "projects/{{project}}/locations/{{location}}/namespaces"},
	"google_recaptcha_enterprise_key": {Service: svcRecaptcha,
		Collection: "projects/{{project}}/keys"},
	"google_active_directory_domain": {Service: svcManagedAD,
		Collection: "projects/{{project}}/locations/global/domains"},
}

// NotCovered is each labelled server-assigned type with no per-type lister, and why, as choudoufu's gcpsweep names
// them. They bind through Asset Inventory.
var NotCovered = map[string]string{
	"google_dialogflow_cx_intent":       "its list is under its agent (parent), which is another resource's attribute at plan time",
	"google_assured_workloads_workload": "its list is under an organization, and the scope a list is made in is a project",
}

// serverAssigned returns tfType's written-out list, if it is one of [ServerAssigned].
func serverAssigned(tfType string) (Type, bool) {
	t, ok := ServerAssigned[tfType]
	if !ok {
		return Type{}, false
	}
	t.TFType = tfType
	return t, true
}
