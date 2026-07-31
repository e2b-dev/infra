package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

// TestOpenAPIIncludesHandlerContract ensures every status written directly by
// a generated API handler is documented and every body it requires is marked
// required by that operation.
func TestOpenAPIIncludesHandlerContract(t *testing.T) {
	t.Parallel()

	spec, err := api.GetSpec()
	require.NoError(t, err)

	operationsByHandler := generatedOperationsByHandler(t, spec)
	require.NotEmpty(t, operationsByHandler)

	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	pkg := packages["handlers"]
	require.NotNil(t, pkg)
	functionsByName := make(map[string]*ast.FuncDecl)
	for _, file := range pkg.Files {
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Body != nil {
				functionsByName[function.Name.Name] = function
			}
		}
	}

	for _, file := range pkg.Files {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Body == nil {
				continue
			}

			operation, isOperation := operationsByHandler[function.Name.Name]
			if !isOperation {
				continue
			}

			inspectFunctionCalls(function, functionsByName, func(call *ast.CallExpr) {
				selector := calledSelector(call.Fun)
				if selector != nil && selector.Sel.Name == "ParseBody" {
					require.NotNil(t, operation.RequestBody,
						"%s requires a request body but OpenAPI does not declare one", function.Name.Name)
					require.NotNil(t, operation.RequestBody.Value)
					require.True(t, operation.RequestBody.Value.Required,
						"%s rejects an absent body but OpenAPI marks it optional", function.Name.Name)
				}

				if selector == nil {
					return
				}

				_, ok = selector.X.(*ast.Ident)
				if !ok || len(call.Args) == 0 {
					return
				}

				statusArgument := -1
				switch selector.Sel.Name {
				case "JSON", "Status", "String", "Data":
					statusArgument = 0
				case "sendAPIStoreError":
					statusArgument = 1
				}
				if statusArgument < 0 || statusArgument >= len(call.Args) {
					return
				}

				statusSelector, ok := call.Args[statusArgument].(*ast.SelectorExpr)
				if !ok {
					return
				}
				packageName, ok := statusSelector.X.(*ast.Ident)
				if !ok || packageName.Name != "http" {
					return
				}

				status := statusCode(statusSelector.Sel.Name)
				require.NotZero(t, status, "unrecognized HTTP status constant %s", statusSelector.Sel.Name)
				response := operation.Responses.Value(strconv.Itoa(status))
				require.NotNil(t, response,
					"%s writes HTTP %d but its OpenAPI operation does not declare that response",
					function.Name.Name, status)
				require.NotNil(t, response.Value)

				switch selector.Sel.Name {
				case "JSON", "sendAPIStoreError":
					require.Contains(t, response.Value.Content, "application/json",
						"%s writes an application/json HTTP %d response but OpenAPI declares different content",
						function.Name.Name, status)
				case "String":
					require.Contains(t, response.Value.Content, "text/plain",
						"%s writes a text/plain HTTP %d response but OpenAPI declares different content",
						function.Name.Name, status)
				case "Status":
					require.Empty(t, response.Value.Content,
						"%s writes an empty HTTP %d response but OpenAPI declares response content",
						function.Name.Name, status)
				}

			})
		}
	}
}

func inspectFunctionCalls(
	function *ast.FuncDecl,
	functionsByName map[string]*ast.FuncDecl,
	inspect func(*ast.CallExpr),
) {
	visited := make(map[string]bool)
	var walk func(*ast.FuncDecl)
	walk = func(current *ast.FuncDecl) {
		if visited[current.Name.Name] {
			return
		}
		visited[current.Name.Name] = true
		receiverName := ""
		if current.Recv != nil && len(current.Recv.List) == 1 && len(current.Recv.List[0].Names) == 1 {
			receiverName = current.Recv.List[0].Names[0].Name
		}

		ast.Inspect(current.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			inspect(call)

			var calledName string
			switch called := call.Fun.(type) {
			case *ast.Ident:
				calledName = called.Name
			case *ast.SelectorExpr:
				receiver, ok := called.X.(*ast.Ident)
				if ok && receiver.Name == receiverName {
					calledName = called.Sel.Name
				}
			}
			if calledFunction := functionsByName[calledName]; calledFunction != nil {
				walk(calledFunction)
			}

			return true
		})
	}

	walk(function)
}

// generatedOperationsByHandler reads the generated route registrations rather
// than OperationID: this spec intentionally omits operationId and oapi-codegen
// derives Go handler names from each method and path.
func generatedOperationsByHandler(t *testing.T, spec *openapi3.T) map[string]*openapi3.Operation {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "../api/api.gen.go", nil, 0)
	require.NoError(t, err)

	operations := make(map[string]*openapi3.Operation)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}

		methodSelector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		router, ok := methodSelector.X.(*ast.Ident)
		if !ok || router.Name != "router" {
			return true
		}

		pathExpression, ok := call.Args[0].(*ast.BinaryExpr)
		if !ok {
			return true
		}
		pathLiteral, ok := pathExpression.Y.(*ast.BasicLit)
		if !ok || pathLiteral.Kind != token.STRING {
			return true
		}
		path, err := strconv.Unquote(pathLiteral.Value)
		require.NoError(t, err)
		path = regexp.MustCompile(`:([A-Za-z0-9]+)`).ReplaceAllString(path, `{$1}`)

		handlerSelector, ok := call.Args[1].(*ast.SelectorExpr)
		if !ok {
			return true
		}
		wrapper, ok := handlerSelector.X.(*ast.Ident)
		if !ok || wrapper.Name != "wrapper" {
			return true
		}

		pathItem := spec.Paths.Find(path)
		require.NotNil(t, pathItem, "generated route %s is absent from OpenAPI", path)
		operation := pathItem.GetOperation(strings.ToUpper(methodSelector.Sel.Name))
		require.NotNil(t, operation, "generated route %s %s is absent from OpenAPI", methodSelector.Sel.Name, path)
		operations[handlerSelector.Sel.Name] = operation

		return true
	})

	return operations
}

func calledSelector(expression ast.Expr) *ast.SelectorExpr {
	switch value := expression.(type) {
	case *ast.SelectorExpr:
		return value
	case *ast.IndexExpr:
		return calledSelector(value.X)
	case *ast.IndexListExpr:
		return calledSelector(value.X)
	default:
		return nil
	}
}

func statusCode(name string) int {
	switch name {
	case "StatusOK":
		return http.StatusOK
	case "StatusCreated":
		return http.StatusCreated
	case "StatusAccepted":
		return http.StatusAccepted
	case "StatusNoContent":
		return http.StatusNoContent
	case "StatusBadRequest":
		return http.StatusBadRequest
	case "StatusUnauthorized":
		return http.StatusUnauthorized
	case "StatusForbidden":
		return http.StatusForbidden
	case "StatusNotFound":
		return http.StatusNotFound
	case "StatusConflict":
		return http.StatusConflict
	case "StatusGone":
		return http.StatusGone
	case "StatusTooManyRequests":
		return http.StatusTooManyRequests
	case "StatusInternalServerError":
		return http.StatusInternalServerError
	case "StatusNotImplemented":
		return http.StatusNotImplemented
	case "StatusBadGateway":
		return http.StatusBadGateway
	case "StatusServiceUnavailable":
		return http.StatusServiceUnavailable
	case "StatusGatewayTimeout":
		return http.StatusGatewayTimeout
	default:
		return 0
	}
}
