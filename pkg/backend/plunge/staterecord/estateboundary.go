// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import _ "embed"

// estate-boundary.yaml is choudoufu's live/kubernetes/estate-boundary.yaml,
// carried beside the store because an embed directive can only name files
// under its own package's directory. In choudoufu this lives in its own
// package (github.com/intentius/choudoufu/live/kubernetes, embed.go); plunge
// has no copy of that directory, so the embed moves here. See NOTICE.
//
//go:embed estate-boundary.yaml
var estateBoundaryYAML []byte

// EstateBoundaryYAML is estate-boundary.yaml as shipped: one multi-document
// YAML file holding the ValidatingAdmissionPolicy a cluster admin installs
// and the binding that puts it in force.
//
// The copy is the caller's; the embedded bytes are not handed out, because a
// caller that wrote through them would change every later reader's answer.
func EstateBoundaryYAML() []byte {
	return append([]byte(nil), estateBoundaryYAML...)
}
