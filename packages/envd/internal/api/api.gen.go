// Package api provides primitives to interact with the openapi HTTP API.
//
// Code generated by github.com/deepmap/oapi-codegen version v1.16.2 DO NOT EDIT.
package api

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/oapi-codegen/runtime"
)

// ServerInterface represents all server handlers.
type ServerInterface interface {
	// Download a file
	// (GET /filesystem/files/{path})
	GetFilesystemFilesPath(ctx echo.Context, path FilePath) error
	// Upload a file
	// (PUT /filesystem/files/{path})
	PutFilesystemFilesPath(ctx echo.Context, path FilePath, params PutFilesystemFilesPathParams) error
	// Check the health of the envd
	// (GET /health)
	GetHealth(ctx echo.Context) error
	// Ensure the time and metadata is synced with the host
	// (POST /host/sync)
	PostHostSync(ctx echo.Context) error
}

// ServerInterfaceWrapper converts echo contexts to parameters.
type ServerInterfaceWrapper struct {
	Handler ServerInterface
}

// GetFilesystemFilesPath converts echo context to params.
func (w *ServerInterfaceWrapper) GetFilesystemFilesPath(ctx echo.Context) error {
	var err error
	// ------------- Path parameter "path" -------------
	var path FilePath

	err = runtime.BindStyledParameterWithLocation("simple", false, "path", runtime.ParamLocationPath, ctx.Param("path"), &path)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Invalid format for parameter path: %s", err))
	}

	// Invoke the callback with all the unmarshaled arguments
	err = w.Handler.GetFilesystemFilesPath(ctx, path)
	return err
}

// PutFilesystemFilesPath converts echo context to params.
func (w *ServerInterfaceWrapper) PutFilesystemFilesPath(ctx echo.Context) error {
	var err error
	// ------------- Path parameter "path" -------------
	var path FilePath

	err = runtime.BindStyledParameterWithLocation("simple", false, "path", runtime.ParamLocationPath, ctx.Param("path"), &path)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Invalid format for parameter path: %s", err))
	}

	// Parameter object where we will unmarshal all parameters from the context
	var params PutFilesystemFilesPathParams
	// ------------- Optional query parameter "User" -------------

	err = runtime.BindQueryParameter("form", true, false, "User", ctx.QueryParams(), &params.User)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Invalid format for parameter User: %s", err))
	}

	// ------------- Optional query parameter "Mode" -------------

	err = runtime.BindQueryParameter("form", true, false, "Mode", ctx.QueryParams(), &params.Mode)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Invalid format for parameter Mode: %s", err))
	}

	// ------------- Optional query parameter "overwrite" -------------

	err = runtime.BindQueryParameter("form", true, false, "overwrite", ctx.QueryParams(), &params.Overwrite)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Invalid format for parameter overwrite: %s", err))
	}

	// Invoke the callback with all the unmarshaled arguments
	err = w.Handler.PutFilesystemFilesPath(ctx, path, params)
	return err
}

// GetHealth converts echo context to params.
func (w *ServerInterfaceWrapper) GetHealth(ctx echo.Context) error {
	var err error

	// Invoke the callback with all the unmarshaled arguments
	err = w.Handler.GetHealth(ctx)
	return err
}

// PostHostSync converts echo context to params.
func (w *ServerInterfaceWrapper) PostHostSync(ctx echo.Context) error {
	var err error

	// Invoke the callback with all the unmarshaled arguments
	err = w.Handler.PostHostSync(ctx)
	return err
}

// This is a simple interface which specifies echo.Route addition functions which
// are present on both echo.Echo and echo.Group, since we want to allow using
// either of them for path registration
type EchoRouter interface {
	CONNECT(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	DELETE(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	GET(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	HEAD(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	OPTIONS(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	PATCH(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	POST(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	PUT(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	TRACE(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
}

// RegisterHandlers adds each server route to the EchoRouter.
func RegisterHandlers(router EchoRouter, si ServerInterface) {
	RegisterHandlersWithBaseURL(router, si, "")
}

// Registers handlers, and prepends BaseURL to the paths, so that the paths
// can be served under a prefix.
func RegisterHandlersWithBaseURL(router EchoRouter, si ServerInterface, baseURL string) {

	wrapper := ServerInterfaceWrapper{
		Handler: si,
	}

	router.GET(baseURL+"/filesystem/files/:path", wrapper.GetFilesystemFilesPath)
	router.PUT(baseURL+"/filesystem/files/:path", wrapper.PutFilesystemFilesPath)
	router.GET(baseURL+"/health", wrapper.GetHealth)
	router.POST(baseURL+"/host/sync", wrapper.PostHostSync)

}