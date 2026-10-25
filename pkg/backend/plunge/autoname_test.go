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

// The autonaming acceptance tests of INTENTIUS/plunge#7 (option A, claim C1), with a deploytest provider standing
// in for pulumi-aws's aws_iam_role, whose name the bridge autonames and choudoufu's pkg/identity builds the
// import identity from:
//
//   - the same inputs give the same name across two runs on two fresh stores (two machines);
//   - the name respects a maximum length and keeps upstream's charset;
//   - a replacement of a deterministic-name resource deletes first, and says so once;
//   - a user pattern with a random part is refused by name, unless the opt-out is set;
//   - a type with no uniqueness evidence keeps upstream's autonaming;
//   - the loser of a racing create fails at the cloud, and its re-run binds the winner by marker.
//
// Written for this unit and not run in it; the maintainer runs them.

import (
	"context"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/awsnative"
	"github.com/pulumi/pulumi/pkg/v3/backend/plunge/markers"
	"github.com/pulumi/pulumi/pkg/v3/engine"
	"github.com/pulumi/pulumi/pkg/v3/resource/autonaming"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy/deploytest"
	"github.com/pulumi/pulumi/pkg/v3/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

const roleType = "aws:iam/role:Role"

// roleMapping is pulumi-aws's mapping cut down to aws_iam_role: name is autonamed and forces a new resource, path
// forces a new resource, tags is an updatable map. The bucket is there with no autonamed field.
const roleMapping = `{
  "provider": {"resources": {
    "aws_iam_role": {
      "name": {"type": 4, "optional": true, "computed": true, "forceNew": true},
      "path": {"type": 4, "optional": true, "forceNew": true},
      "tags": {"type": 6, "optional": true, "element": {"schema": {"type": 4}}}
    },
    "aws_s3_bucket": {"bucket": {"type": 4, "optional": true}, "tags": {"type": 6, "optional": true, "element": {"schema": {"type": 4}}}}
  }},
  "resources": {
    "aws_iam_role": {"tok": "aws:iam/role:Role", "fields": {"name": {"default": {"autonamed": true}}}},
    "aws_s3_bucket": {"tok": "aws:s3/bucket:Bucket", "fields": {}}
  }
}`

// fakeCloud holds roles by name and refuses a second role with the same name, as IAM does.
type fakeCloud struct {
	mu     sync.Mutex
	byName map[string]resource.PropertyMap
	// ops is every create and delete, in order: "create <name>", "delete <name>".
	ops []string
	// checked is every autonaming option a Check was given, by URN name; nil entries are "none".
	checked map[string][]*plugin.AutonamingOptions
}

func newFakeCloud() *fakeCloud {
	return &fakeCloud{byName: map[string]resource.PropertyMap{}, checked: map[string][]*plugin.AutonamingOptions{}}
}

func (c *fakeCloud) loaders() []*deploytest.ProviderLoader {
	return []*deploytest.ProviderLoader{
		deploytest.NewProviderLoader("aws", semver.MustParse("7.0.0"), func() (plugin.Provider, error) {
			return c.provider(), nil
		}),
	}
}

// provider names a role the way the bridge does: the program's name, else the proposed name, else the prior
// name, else the URN's name and a random suffix from the seed.
func (c *fakeCloud) provider() *deploytest.Provider {
	return &deploytest.Provider{
		Version: semver.MustParse("7.0.0"),
		GetMappingF: func(_ context.Context, req plugin.GetMappingRequest) (plugin.GetMappingResponse, error) {
			if req.Key != "terraform" {
				return plugin.GetMappingResponse{}, nil
			}
			return plugin.GetMappingResponse{Provider: "aws", Data: []byte(roleMapping)}, nil
		},
		CheckF: func(_ context.Context, req plugin.CheckRequest) (plugin.CheckResponse, error) {
			c.mu.Lock()
			c.checked[req.URN.Name()] = append(c.checked[req.URN.Name()], req.Autonaming)
			c.mu.Unlock()
			inputs := resource.ToResourcePropertyMap(req.NewInputs)
			key := resource.PropertyKey("name")
			if req.URN.Type() == bucketType {
				key = "bucket"
			}
			if _, ok := inputs[key]; !ok {
				old := resource.ToResourcePropertyMap(req.OldInputs)
				switch {
				case old[key].IsString():
					inputs[key] = old[key]
				case req.Autonaming != nil && req.Autonaming.Mode != plugin.AutonamingModeDisabled:
					inputs[key] = resource.NewProperty(req.Autonaming.ProposedName)
				default:
					inputs[key] = resource.NewProperty(req.URN.Name() + "-" + hex.EncodeToString(req.RandomSeed)[:7])
				}
			}
			return plugin.CheckResponse{Properties: resource.FromResourcePropertyMap(inputs)}, nil
		},
		DiffF: func(_ context.Context, req plugin.DiffRequest) (plugin.DiffResult, error) {
			olds, news := resource.ToResourcePropertyMap(req.OldInputs), resource.ToResourcePropertyMap(req.NewInputs)
			if !olds["path"].DeepEquals(news["path"]) {
				return plugin.DiffResult{Changes: plugin.DiffSome, ReplaceKeys: []resource.PropertyKey{"path"}}, nil
			}
			if !olds.DeepEquals(news) {
				return plugin.DiffResult{Changes: plugin.DiffSome}, nil
			}
			return plugin.DiffResult{Changes: plugin.DiffNone}, nil
		},
		CreateF: func(_ context.Context, req plugin.CreateRequest) (plugin.CreateResponse, error) {
			props := resource.ToResourcePropertyMap(req.Properties)
			name := props["name"].StringValue()
			if req.URN.Type() == bucketType {
				name = props["bucket"].StringValue()
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if _, ok := c.byName[name]; ok {
				return plugin.CreateResponse{}, fmt.Errorf("EntityAlreadyExists: Role with name %s already exists", name)
			}
			c.byName[name] = props
			c.ops = append(c.ops, "create "+name)
			return plugin.CreateResponse{ID: resource.ID(name), Properties: req.Properties, Status: resource.StatusOK}, nil
		},
		UpdateF: func(_ context.Context, req plugin.UpdateRequest) (plugin.UpdateResponse, error) {
			c.mu.Lock()
			c.byName[string(req.ID)] = resource.ToResourcePropertyMap(req.NewInputs)
			c.mu.Unlock()
			return plugin.UpdateResponse{Properties: req.NewInputs, Status: resource.StatusOK}, nil
		},
		DeleteF: func(_ context.Context, req plugin.DeleteRequest) (plugin.DeleteResponse, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			delete(c.byName, string(req.ID))
			c.ops = append(c.ops, "delete "+string(req.ID))
			return plugin.DeleteResponse{Status: resource.StatusOK}, nil
		},
		ReadF: func(_ context.Context, req plugin.ReadRequest) (plugin.ReadResponse, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			props, ok := c.byName[string(req.ID)]
			if !ok {
				return plugin.ReadResponse{Status: resource.StatusOK}, nil
			}
			m := resource.FromResourcePropertyMap(props)
			return plugin.ReadResponse{
				ReadResult: plugin.ReadResult{ID: req.ID, Inputs: &m, Outputs: &m},
				Status:     resource.StatusOK,
			}, nil
		},
	}
}

func (c *fakeCloud) names() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for n := range c.byName {
		out = append(out, n)
	}
	return out
}

// roleProgram registers one role, with no name, at path.
func roleProgram(path string) deploytest.LanguageRuntimeFactory {
	return deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		_, err := monitor.RegisterResource(roleType, "app", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{
				"path": path,
				"tags": map[string]any{"team": "a"},
			}),
		})
		return err
	})
}

func roleURN() resource.URN {
	return resource.NewURN("test", "test", "", tokens.Type(roleType), "app")
}

func roleInfo(t *testing.T) TypeInfo {
	t.Helper()
	p := newFakeCloud().provider()
	info := newCatalog().lookup(context.Background(), p, roleType)
	require.True(t, info.Autoname.Unique, info.Autoname.Uncovered)
	require.Equal(t, "name", info.Autoname.Property)
	return info
}

// Two runs, on two fresh stores and two fresh clouds (two machines, nothing shared), name the role the same, and
// the name is DeterministicName's.
func TestDeterministicNameIsTheSameAcrossRuns(t *testing.T) {
	t.Parallel()

	var got []string
	for i := 0; i < 2; i++ {
		cloud := newFakeCloud()
		marks := newLedger()
		_, err := run(t, newCountingStore(t), deploytest.NewPluginHostF(nil, nil, roleProgram("/"), nil, nil,
			cloud.loaders()...), runConfig{markers: true, persist: true, marks: marks})
		require.NoError(t, err)
		require.Len(t, cloud.names(), 1)
		got = append(got, cloud.names()[0])

		e, ok := marks.get(roleURN())
		require.True(t, ok)
		require.NotNil(t, e.Autoname)
		assert.Equal(t, AutonamePlunge, e.Autoname.By)
		assert.True(t, e.Autoname.C1)
	}
	assert.Equal(t, got[0], got[1])
	assert.Equal(t, DeterministicName(roleURN(), 0, ""), got[0])

	// The name depends on the stack and the project, so two stacks sharing an account do not collide.
	other := resource.NewURN("prod", "test", "", tokens.Type(roleType), "app")
	assert.NotEqual(t, DeterministicName(roleURN(), 0, ""), DeterministicName(other, 0, ""))
	assert.Equal(t, DeterministicName(other, 0, ""), DeterministicName(other, 0, ""))
}

// A name longer than the schema's maximum is cut to fit, room is left for the provider's suffix, the hash is kept,
// and the characters are the URN name's plus a dash and lowercase hex, as upstream's default autoname has.
func TestDeterministicNameRespectsLengthAndCharset(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 80)
	urnA := resource.NewURN("test", "test", "", "aws-native:sqs:Queue", long+"x")
	urnB := resource.NewURN("test", "test", "", "aws-native:sqs:Queue", long+"y")

	a := DeterministicName(urnA, 80, ".fifo")
	b := DeterministicName(urnB, 80, ".fifo")
	assert.LessOrEqual(t, len(a), 80-len(".fifo"))
	assert.LessOrEqual(t, len(b), 80-len(".fifo"))
	assert.NotEqual(t, a, b, "names cut to the same prefix still differ by their hash")
	assert.Equal(t, a, DeterministicName(urnA, 80, ".fifo"))
	assert.Regexp(t, regexp.MustCompile(`^a+-[0-9a-f]{7}$`), a)

	short := DeterministicName(roleURN(), 64, "")
	assert.Regexp(t, regexp.MustCompile(`^app-[0-9a-f]{7}$`), short)

	// A maximum too short for any of the name gives the hash alone.
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{5}$`), DeterministicName(roleURN(), 5, ""))

	// A multi-byte name is cut at a character boundary.
	wideName := strings.Repeat("é", 40)
	wide := resource.NewURN("test", "test", "", tokens.Type(roleType), wideName)
	hash := DeterministicName(wide, 0, "")[len(wideName)+1:]
	cut := DeterministicName(wide, 20, "")
	assert.True(t, utf8.ValidString(cut), cut)
	assert.LessOrEqual(t, len(cut), 20)
	assert.True(t, strings.HasSuffix(cut, "-"+hash), cut)

	// aws-native's metadata bounds reach the policy: AWS::IAM::Role's roleName is at most 64 characters.
	md, err := awsnative.Default.Load(context.Background(), awsnative.EmbeddedVersion)
	require.NoError(t, err)
	var info TypeInfo
	info.Type, info.Family = "aws-native:iam:Role", FamilyAWSNative
	nativeInfo(md, &info)
	assert.True(t, info.Autoname.Unique, info.Autoname.Uncovered)
	assert.Equal(t, "roleName", info.Autoname.Property)
	assert.Equal(t, 64, info.Autoname.MaxLength)
}

// A replacement of the role (its path forces a new one) deletes the old role before creating the new one, though
// the provider did not ask for that, and the run warns once naming the URN.
func TestReplaceOfADeterministicNameIsDeleteBeforeReplace(t *testing.T) {
	t.Parallel()

	cloud := newFakeCloud()
	store := newCountingStore(t)
	_, err := run(t, store, deploytest.NewPluginHostF(nil, nil, roleProgram("/"), nil, nil, cloud.loaders()...),
		runConfig{markers: true, persist: true})
	require.NoError(t, err)
	name := DeterministicName(roleURN(), 0, "")
	require.Equal(t, []string{"create " + name}, cloud.ops)

	events, err := run(t, store, deploytest.NewPluginHostF(nil, nil, roleProgram("/service/"), nil, nil,
		cloud.loaders()...), runConfig{markers: true, persist: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"create " + name, "delete " + name, "create " + name}, cloud.ops,
		"the old role is deleted before its replacement, which has the same name")

	var ops []string
	for _, m := range steps(events)["app"] {
		ops = append(ops, string(m.Op))
	}
	assert.Contains(t, ops, string(deploy.OpDeleteReplaced))
	assert.Contains(t, ops, string(deploy.OpCreateReplacement))

	warnings := 0
	for _, e := range events {
		if p, ok := e.Payload().(engine.DiagEventPayload); ok && p.Severity == diag.Warning &&
			strings.Contains(p.Message, "deterministic name") {
			warnings++
			assert.Contains(t, p.Message, string(roleURN()))
		}
	}
	assert.Equal(t, 1, warnings)
}

// A user pattern with a random part on a covered type is refused, naming the resource and the random part; with
// the opt-out set it stands and the record says C1 does not cover the resource. A deterministic user pattern
// stands with no refusal.
func TestRandomUserPatternIsRefused(t *testing.T) {
	t.Parallel()

	user := &autonaming.Global{Default: &autonaming.Pattern{Pattern: "${name}-${hex(8)}"}}
	random := autonaming.RandomExpressions(user, roleType)
	require.Equal(t, []string{"${hex(8)}"}, random)
	assert.Empty(t, autonaming.RandomExpressions(&autonaming.Global{Default: &autonaming.Pattern{
		Pattern: "${project}-${stack}-${name}",
	}}, roleType))

	req := deploy.AutonameRequest{
		URN:        roleURN(),
		Type:       roleType,
		Provider:   newFakeCloud().provider(),
		Inputs:     taggedInputs(t, roleInfo(t)),
		User:       &plugin.AutonamingOptions{ProposedName: "app-0a1b2c3d", Mode: plugin.AutonamingModePropose},
		UserRandom: random,
	}
	_, _, err := newAutonamePolicy(newCatalog(), newLedger()).Autoname(context.Background(), req)
	var refused *RandomAutonamePatternError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, err.Error(), string(roleURN()))
	assert.Contains(t, err.Error(), "${hex(8)}")
	assert.Contains(t, err.Error(), EnvAllowRandomAutonames)

	marks := newLedger()
	optedOut := newAutonamePolicy(newCatalog(), marks)
	optedOut.allowRandom = true
	opts, deleteFirst, err := optedOut.Autoname(context.Background(), req)
	require.NoError(t, err)
	assert.Nil(t, opts, "the user's options stand")
	assert.False(t, deleteFirst)
	e, _ := marks.get(roleURN())
	require.NotNil(t, e.Autoname)
	assert.Equal(t, AutonameUser, e.Autoname.By)
	assert.False(t, e.Autoname.C1)
	assert.Contains(t, e.Autoname.Uncovered, EnvAllowRandomAutonames)

	req.UserRandom = nil
	opts, _, err = newAutonamePolicy(newCatalog(), newLedger()).Autoname(context.Background(), req)
	require.NoError(t, err)
	assert.Nil(t, opts)

	// A name the program sets itself wins over everything, and is not refused.
	req.UserRandom = random
	req.Inputs["name"] = resource.NewProperty("my-role")
	opts, deleteFirst, err = newAutonamePolicy(newCatalog(), newLedger()).Autoname(context.Background(), req)
	require.NoError(t, err)
	assert.Nil(t, opts)
	assert.False(t, deleteFirst)
}

// The bucket has no autonamed property in its mapping, so it keeps upstream's autonaming: Check is given no
// autonaming options, the name has the random suffix, and the record says why C1 does not cover it.
func TestUncoveredTypeKeepsUpstreamAutonaming(t *testing.T) {
	t.Parallel()

	cloud := newFakeCloud()
	marks := newLedger()
	program := deploytest.NewLanguageRuntimeF(func(_ plugin.RunInfo, monitor *deploytest.ResourceMonitor) error {
		_, err := monitor.RegisterResource(bucketType, "logs", true, deploytest.ResourceOptions{
			Inputs: resource.NewPropertyMapFromMap(map[string]any{"tags": map[string]any{"team": "a"}}),
		})
		return err
	})
	_, err := run(t, newCountingStore(t), deploytest.NewPluginHostF(nil, nil, program, nil, nil,
		cloud.loaders()...), runConfig{markers: true, persist: true, marks: marks})
	require.NoError(t, err)

	require.NotEmpty(t, cloud.checked["logs"])
	for _, o := range cloud.checked["logs"] {
		assert.Nil(t, o)
	}
	require.Len(t, cloud.names(), 1)
	assert.NotEqual(t, DeterministicName(bucketURN(), 0, ""), cloud.names()[0])

	e, ok := marks.get(bucketURN())
	require.True(t, ok)
	require.NotNil(t, e.Autoname)
	assert.Equal(t, AutonameUpstream, e.Autoname.By)
	assert.False(t, e.Autoname.C1)
	assert.Contains(t, e.Autoname.Uncovered, "marks no property")
}

// Two runs race to create the role. The winner's create lands; the loser's bind ran before it did, so its create
// asks the cloud for the same name and is refused. The loser's re-run binds the winner's object by its markers
// and creates nothing. The loser runs on its own store, which is the case where the winner's record is not there
// to find (the winner stopped after its create): with the record there, the re-run plans against it directly.
func TestRacingCreateLoserBindsTheWinnerOnReRun(t *testing.T) {
	t.Parallel()

	cloud := newFakeCloud()
	_, err := run(t, newCountingStore(t), deploytest.NewPluginHostF(nil, nil, roleProgram("/"), nil, nil,
		cloud.loaders()...), runConfig{markers: true, persist: true})
	require.NoError(t, err)
	name := DeterministicName(roleURN(), 0, "")

	loser := newCountingStore(t)
	_, err = run(t, loser, deploytest.NewPluginHostF(nil, nil, roleProgram("/"), nil, nil, cloud.loaders()...),
		runConfig{markers: true, persist: true, listers: map[Family]Lister{FamilyAWS: &fakeLister{}}})
	require.Error(t, err, "the cloud refuses a second role with the same name")
	assert.Equal(t, []string{"create " + name}, cloud.ops)

	want, err := markers.For(roleURN(), markers.EncodingTags)
	require.NoError(t, err)
	tags := map[string]string{"team": "a"}
	for k, v := range want {
		tags[k] = v
	}
	winner := &fakeLister{objs: []LiveObject{{ID: resource.ID(name), Tags: tags}}}
	marks := newLedger()
	_, err = run(t, loser, deploytest.NewPluginHostF(nil, nil, roleProgram("/"), nil, nil, cloud.loaders()...),
		runConfig{markers: true, persist: true, marks: marks, listers: map[Family]Lister{FamilyAWS: winner}})
	require.NoError(t, err)
	assert.Equal(t, []string{"create " + name}, cloud.ops, "the re-run created nothing")
	e, ok := marks.get(roleURN())
	require.True(t, ok)
	assert.Equal(t, resource.ID(name), e.Bound)
}

// taggedInputs are the role's inputs with its markers merged in, as the injector leaves them.
func taggedInputs(t *testing.T, info TypeInfo) resource.PropertyMap {
	t.Helper()
	want, err := markers.For(roleURN(), markers.EncodingTags)
	require.NoError(t, err)
	out, ok := mergeMarkers(resource.NewPropertyMapFromMap(map[string]any{
		"path": "/", "tags": map[string]any{"team": "a"},
	}), info, want)
	require.True(t, ok)
	return out
}
