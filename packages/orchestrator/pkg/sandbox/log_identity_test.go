//go:build linux

package sandbox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// identityFieldHelpers are the logger fields that RuntimeMetadata.LogFields
// already contributes. Passing one to a logger that carries them emits the key
// twice: zap keeps both, and a JSON consumer picks whichever it likes.
var identityFieldHelpers = map[string]bool{
	"WithSandboxID":   true,
	"WithTemplateID":  true,
	"WithTeamID":      true,
	"WithBuildID":     true,
	"WithExecutionID": true,
}

var logLevelMethods = map[string]bool{
	"Debug": true,
	"Info":  true,
	"Warn":  true,
	"Error": true,
	"Fatal": true,
	"Panic": true,
	"Log":   true,
}

// TestNoDuplicateIdentityFields fails the build when a log call adds an
// identity field to a logger that already carries one.
//
// This is a source-level guard because nothing else catches it: duplicate zap
// keys are not a compile error, not a vet finding, and not visible to a test
// that only exercises behaviour. Both instances found so far reached review —
// one an explicit WithSandboxID left behind by a mechanical conversion, one a
// buildID threaded into a helper purely to log it.
//
// If a line legitimately describes a *different* sandbox than the logger's
// own, do not add an exemption: reach for logger.L() explicitly, so the
// mismatch is visible at the call site.
func TestNoDuplicateIdentityFields(t *testing.T) {
	t.Parallel()

	files := packageFiles(t)
	require.NotEmpty(t, files, "found no sources to scan; is the test running outside the package directory?")

	scanned := 0

	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		require.NoErrorf(t, err, "parse %s", path)

		carriers := identityLoggerVars(file)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			// With is checked as well as the log levels: fields attached
			// there reach every line the derived logger writes, so
			// lg.With(logger.WithSandboxID(id)).Info(...) duplicates the key
			// just as surely as passing it to Info directly.
			if !logLevelMethods[method.Sel.Name] && method.Sel.Name != "With" {
				return true
			}

			if !carriesIdentity(method.X, carriers) {
				return true
			}

			scanned++

			for _, arg := range call.Args {
				helper, ok := arg.(*ast.CallExpr)
				if !ok {
					continue
				}

				sel, ok := helper.Fun.(*ast.SelectorExpr)
				if !ok || !identityFieldHelpers[sel.Sel.Name] {
					continue
				}

				if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "logger" {
					continue
				}

				assert.Failf(t, "duplicate identity field",
					"%s: logger.%s on a logger that already carries identity; drop the field",
					fset.Position(arg.Pos()), sel.Sel.Name)
			}

			return true
		})
	}

	// A refactor that renamed the accessors would otherwise leave this test
	// passing while checking nothing.
	assert.Positive(t, scanned, "matched no identity-carrying call sites; the detection below has gone stale")
}

// packageFiles returns the non-test sources of this package and everything
// under it. Subpackages are included: they cannot reach Sandbox.log, but they
// do use handler.Logger() and sbxlogger.I/E, which carry identity the same way.
func packageFiles(t *testing.T) []string {
	t.Helper()

	var out []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		out = append(out, path)

		return nil
	})
	require.NoError(t, err)

	return out
}

// identityLoggerVars collects the names of variables assigned an
// identity-carrying logger, so uses through the variable are checked too.
func identityLoggerVars(file *ast.File) map[string]bool {
	vars := make(map[string]bool)

	// Two passes: an assignment can precede or follow other assignments that
	// make its right-hand side an identity carrier.
	for range 2 {
		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != len(assign.Rhs) {
				return true
			}

			for i, lhs := range assign.Lhs {
				name, ok := lhs.(*ast.Ident)
				if !ok || !carriesIdentity(assign.Rhs[i], vars) {
					continue
				}

				vars[name.Name] = true
			}

			return true
		})
	}

	return vars
}

// carriesIdentity reports whether expr evaluates to a logger already tagged
// with the sandbox's identity fields. Every field named logger in this tree
// holds one that does; a plain logger would make this over-report, which is a
// visible test failure rather than a silent gap.
func carriesIdentity(expr ast.Expr, vars map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return vars[e.Name]

	// A logger read straight out of a struct field: u.logger, p.logger. This
	// is how an injected logger is held, so it is the dominant shape in the
	// subpackages — the uffd serve loop, the prefetcher, the counter reporter.
	case *ast.SelectorExpr:
		return e.Sel.Name == "logger"

	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}

		switch sel.Sel.Name {
		// Sandbox.log, RuntimeMetadata.Logger, Userfaultfd.Logger.
		case "log", "Logger":
			return len(e.Args) == 0

		// sbxlogger.I / sbxlogger.E tag from SandboxMetadata.Fields.
		case "I", "E":
			pkg, ok := sel.X.(*ast.Ident)

			return ok && pkg.Name == "sbxlogger"

		// Fields added to an identity logger keep the identity.
		case "With":
			return carriesIdentity(sel.X, vars)
		}
	}

	return false
}
