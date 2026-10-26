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
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ExitPlanMoved differs from every Exit* code in the CLI's taxonomy (INTENTIUS/plunge#11). The constants are read
// from the source file, so a code an upstream merge adds is checked without anyone listing it here.
func TestExitPlanMovedClashesWithNoUpstreamExitCode(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "cmd", "pulumi", "cmd", "exit_codes.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)

	codes := map[string]int{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Exit") {
					continue
				}
				require.Less(t, i, len(vs.Values), "%s has no value of its own; read it by hand", name.Name)
				lit, ok := vs.Values[i].(*ast.BasicLit)
				require.True(t, ok && lit.Kind == token.INT, "%s is not an integer literal; read it by hand", name.Name)
				v, err := strconv.Atoi(lit.Value)
				require.NoError(t, err)
				codes[name.Name] = v
			}
		}
	}

	require.Contains(t, codes, "ExitAuthenticationError", "the taxonomy was not found in %s", path)
	for name, v := range codes {
		assert.NotEqual(t, v, ExitPlanMoved, "ExitPlanMoved (%d) is upstream's %s", ExitPlanMoved, name)
	}
}
