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
	"fmt"
	"sync"

	"github.com/gofrs/uuid"
	"google.golang.org/grpc"

	"github.com/pulumi/pulumi/sdk/v3/go/common/util/rpcutil"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// driftStatusServer is the ResourceStatus service a drift read hands the provider's Read
// (ReadRequest.ResourceStatusAddress), as a refresh step does. A provider that keeps view resources publishes their
// steps to it during Read. A drift read writes no state, so the steps are counted and dropped: what a drift record
// reports is the viewed resource's own outputs.
type driftStatusServer struct {
	pulumirpc.UnimplementedResourceStatusServer

	token  string
	cancel chan bool
	handle rpcutil.ServeHandle

	mu        sync.Mutex
	published int
}

func startDriftStatusServer() (*driftStatusServer, error) {
	token, err := uuid.NewV4()
	if err != nil {
		return nil, fmt.Errorf("plunge: reserving a resource status token: %w", err)
	}
	s := &driftStatusServer{token: token.String(), cancel: make(chan bool)}
	handle, err := rpcutil.ServeWithOptions(rpcutil.ServeOptions{
		Cancel: s.cancel,
		Init: func(srv *grpc.Server) error {
			pulumirpc.RegisterResourceStatusServer(srv, s)
			return nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("plunge: starting the resource status service for a drift read: %w", err)
	}
	s.handle = handle
	return s, nil
}

func (s *driftStatusServer) address() string { return fmt.Sprintf("127.0.0.1:%d", s.handle.Port) }

func (s *driftStatusServer) close() {
	s.cancel <- true
	<-s.handle.Done
}

// PublishViewSteps accepts the view steps of the one resource being read.
func (s *driftStatusServer) PublishViewSteps(
	_ context.Context, req *pulumirpc.PublishViewStepsRequest,
) (*pulumirpc.PublishViewStepsResponse, error) {
	if req.GetToken() != s.token {
		return nil, fmt.Errorf("token %s not found", req.GetToken())
	}
	s.mu.Lock()
	s.published += len(req.GetSteps())
	s.mu.Unlock()
	return &pulumirpc.PublishViewStepsResponse{}, nil
}
