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
	"context"
	"sync"
	"time"

	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// Timings records how long each provider call and each record-store call took, per resource (INTENTIUS/plunge#55).
// The plunge record backend sets Target.Timings for an update that writes records; the provider registry then hands
// out providers wrapped by Wrap, the backend's persister reports its writes through Record, and the engine moves a
// resource's timings onto its resource-outputs or resource-operation-failed event (StepEventMetadata.PlungeTimings)
// once the step's records are written. Nil on every other backend, which keeps upstream's events unchanged.
type Timings struct {
	mu    sync.Mutex
	byURN map[resource.URN][]apitype.PlungeTiming
}

// NewTimings returns an empty recorder.
func NewTimings() *Timings {
	return &Timings{byURN: map[resource.URN][]apitype.PlungeTiming{}}
}

// Record adds one timed call for urn. A nil recorder or an empty URN records nothing.
func (t *Timings) Record(
	urn resource.URN, kind apitype.PlungeTimingKind, call string, start time.Time, d time.Duration, failed bool,
) {
	if t == nil || urn == "" {
		return
	}
	entry := apitype.PlungeTiming{
		Kind:       kind,
		Call:       call,
		StartedAt:  start.UTC().Format(time.RFC3339Nano),
		DurationMs: float64(d.Microseconds()) / 1000,
		Failed:     failed,
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.byURN[urn] = append(t.byURN[urn], entry)
}

// Take returns the calls recorded for urn since the last Take for it, oldest first, and forgets them, so each call
// appears on exactly one event. A replacement's two steps share a URN; each event carries the calls made before it.
func (t *Timings) Take(urn resource.URN) []apitype.PlungeTiming {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	taken := t.byURN[urn]
	delete(t.byURN, urn)
	return taken
}

// Wrap returns p with its resource calls (check, diff, create, read, update, delete) timed into t. Every other
// method passes straight through.
func (t *Timings) Wrap(p plugin.Provider) plugin.Provider {
	if t == nil || p == nil {
		return p
	}
	if already, ok := p.(*timedProvider); ok && already.timings == t {
		return p
	}
	return &timedProvider{Provider: p, timings: t}
}

type timedProvider struct {
	plugin.Provider
	timings *Timings
}

func (p *timedProvider) time(urn resource.URN, call string, start time.Time, err error) {
	p.timings.Record(urn, apitype.PlungeTimingProvider, call, start, time.Since(start), err != nil)
}

func (p *timedProvider) Check(ctx context.Context, req plugin.CheckRequest) (plugin.CheckResponse, error) {
	start := time.Now()
	resp, err := p.Provider.Check(ctx, req)
	p.time(req.URN, "check", start, err)
	return resp, err
}

func (p *timedProvider) Diff(ctx context.Context, req plugin.DiffRequest) (plugin.DiffResponse, error) {
	start := time.Now()
	resp, err := p.Provider.Diff(ctx, req)
	p.time(req.URN, "diff", start, err)
	return resp, err
}

func (p *timedProvider) Create(ctx context.Context, req plugin.CreateRequest) (plugin.CreateResponse, error) {
	start := time.Now()
	resp, err := p.Provider.Create(ctx, req)
	p.time(req.URN, "create", start, err)
	return resp, err
}

func (p *timedProvider) Read(ctx context.Context, req plugin.ReadRequest) (plugin.ReadResponse, error) {
	start := time.Now()
	resp, err := p.Provider.Read(ctx, req)
	p.time(req.URN, "read", start, err)
	return resp, err
}

func (p *timedProvider) Update(ctx context.Context, req plugin.UpdateRequest) (plugin.UpdateResponse, error) {
	start := time.Now()
	resp, err := p.Provider.Update(ctx, req)
	p.time(req.URN, "update", start, err)
	return resp, err
}

func (p *timedProvider) Delete(ctx context.Context, req plugin.DeleteRequest) (plugin.DeleteResponse, error) {
	start := time.Now()
	resp, err := p.Provider.Delete(ctx, req)
	p.time(req.URN, "delete", start, err)
	return resp, err
}

// StepTimings returns the recorder of the deployment step belongs to, or nil when it records none.
func StepTimings(step Step) *Timings {
	if step == nil {
		return nil
	}
	d := step.Deployment()
	if d == nil || d.target == nil {
		return nil
	}
	return d.target.Timings
}

// NewTimedDeploymentForTest returns a bare deployment whose target records into timings, for tests outside this
// package that drive a step's events without running a deployment.
func NewTimedDeploymentForTest(timings *Timings) *Deployment {
	return &Deployment{target: &Target{Timings: timings}}
}
