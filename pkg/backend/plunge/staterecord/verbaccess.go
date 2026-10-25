// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

// VerbAccess is one verb's answer from a SelfSubjectAccessReview.
//
// Vendored from choudoufu's kubernetescontract.go, which plunge does not carry
// (the Kubernetes store is optional in the backend set, see NOTICE). The type
// stays because [Finding] names it.
type VerbAccess struct {
	Verb string `json:"verb"`
	// Allowed is the authorizer's own answer.
	Allowed bool `json:"allowed"`
	// Required is whether this run needs the verb.
	Required bool `json:"required"`
	// Reason is what the authorizer said, when it said anything.
	Reason string `json:"reason,omitempty"`
}
