package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const envdSpecFile = "spec/envd.yaml"

// handRegisteredRoutes are the routes envd serves that its OpenAPI spec does not
// describe, because they are registered straight onto the mux here rather than
// through the generated spec handlers.
//
// Keep this empty if you possibly can. A route in the spec is a route the
// tooling can see: the sandbox proxy builds its list of control-plane routes to
// refuse from the spec's `x-internal` marker, so a route that is missing from the
// spec is invisible to it and ships reachable from any sandbox URL. /upgrade,
// which streams in a replacement envd binary, reached production exactly that
// way.
//
// If you add a route here, decide whether it is control plane. If it is, also add
// it to unspecifiedInternalPaths in packages/orchestrator/pkg/sandbox/envd, or the
// proxy will route it to the internet.
//
// This fork's envd registers everything through the spec, so the list is empty.
// (Upstream's /upgrade stays in the orchestrator's unspecifiedInternalPaths as
// defense-in-depth even though this envd does not serve it.)
var handRegisteredRoutes = []string{}

// chiRoutingMethods are the chi.Router methods that bind a pattern to a handler.
var chiRoutingMethods = map[string]int{
	"Connect":    0,
	"Delete":     0,
	"Get":        0,
	"Handle":     0,
	"HandleFunc": 0,
	"Head":       0,
	"Mount":      0,
	"Options":    0,
	"Patch":      0,
	"Post":       0,
	"Put":        0,
	"Route":      0,
	"Trace":      0,
	// Method(method, pattern, handler) carries the pattern second.
	"Method":     1,
	"MethodFunc": 1,
}

// TestHandRegisteredRoutesAreAccountedFor fails the build when a route is
// registered on envd's mux outside the spec without being declared above.
//
// The spec tests on the proxy side all read the spec, so none of them can see a
// route that never reaches it — which is the one way the currently most dangerous
// route got out. This is the check that looks at what envd actually registers.
func TestHandRegisteredRoutesAreAccountedFor(t *testing.T) {
	t.Parallel()

	found := routePatternsRegisteredInPackage(t)

	assert.ElementsMatch(t, handRegisteredRoutes, found,
		"a route is registered on envd's mux that handRegisteredRoutes does not declare; read the comment on that list before adding to it")
}

// TestHandRegisteredRoutesAreAbsentFromSpec keeps the two route sources from
// overlapping: a pattern in both would register twice on the mux, and chi panics
// on a duplicate route at startup.
func TestHandRegisteredRoutesAreAbsentFromSpec(t *testing.T) {
	t.Parallel()

	doc, err := openapi3.NewLoader().LoadFromFile(envdSpecFile)
	require.NoError(t, err)

	for _, route := range handRegisteredRoutes {
		assert.Nilf(t, doc.Paths.Find(route),
			"%s is in the spec now, so the generated handlers register it; drop it from handRegisteredRoutes and delete the hand-written registration", route)
	}
}

// routePatternsRegisteredInPackage returns the route patterns this package binds
// to handlers.
//
// It reads the source rather than walking a live mux because building envd's real
// router means standing up its services, cgroups and MMDS. The match is a
// heuristic — a chi routing method name, at least two arguments, and a string
// literal starting with "/" — which is why it errs towards reporting too much: an
// extra hit fails loudly and is corrected here, while a miss would be silent.
func routePatternsRegisteredInPackage(t *testing.T) []string {
	t.Helper()

	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", nil, 0)
	require.NoError(t, err)

	pkg, ok := pkgs["main"]
	require.True(t, ok, "package main not found in the envd source directory")

	var patterns []string

	for name, file := range pkg.Files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			pattern, ok := routePattern(n)
			if ok && !slices.Contains(patterns, pattern) {
				patterns = append(patterns, pattern)
			}

			return true
		})
	}

	return patterns
}

func routePattern(n ast.Node) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return "", false
	}

	method, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}

	at, ok := chiRoutingMethods[method.Sel.Name]
	if !ok || len(call.Args) < 2 || at >= len(call.Args) {
		return "", false
	}

	literal, ok := call.Args[at].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}

	pattern, err := strconv.Unquote(literal.Value)
	if err != nil || !strings.HasPrefix(pattern, "/") {
		return "", false
	}

	return pattern, true
}
