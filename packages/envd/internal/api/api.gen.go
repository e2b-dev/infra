// Package api provides primitives to interact with the openapi HTTP API.
//
// Code generated by github.com/deepmap/oapi-codegen/v2 version v2.1.1-0.20240519200907-da9077bb5ffe DO NOT EDIT.
package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/oapi-codegen/runtime"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Error defines model for Error.
type Error struct {
	// Code Error code
	Code int `json:"code"`

	// Message Error message
	Message string `json:"message"`
}

// FilePath defines model for FilePath.
type FilePath = string

// User defines model for User.
type User = string

// DirectoryPathError defines model for DirectoryPathError.
type DirectoryPathError = Error

// FileNotFound defines model for FileNotFound.
type FileNotFound = Error

// InternalServerError defines model for InternalServerError.
type InternalServerError = Error

// InvalidUser defines model for InvalidUser.
type InvalidUser = Error

// NotEnoughDiskSpace defines model for NotEnoughDiskSpace.
type NotEnoughDiskSpace = Error

// GetFilesParams defines parameters for GetFiles.
type GetFilesParams struct {
	// Path Path to the file, URL encoded. Can be relative to user's home directory.
	Path *FilePath `form:"path,omitempty" json:"path,omitempty"`

	// Username User used for setting the owner, or resolving relative paths.
	Username User `form:"username" json:"username"`
}

// PostFilesMultipartBody defines parameters for PostFiles.
type PostFilesMultipartBody struct {
	File *openapi_types.File `json:"file,omitempty"`
}

// PostFilesParams defines parameters for PostFiles.
type PostFilesParams struct {
	// Path Path to the file, URL encoded. Can be relative to user's home directory.
	Path *FilePath `form:"path,omitempty" json:"path,omitempty"`

	// Username User used for setting the owner, or resolving relative paths.
	Username User `form:"username" json:"username"`
}

// PostFilesMultipartRequestBody defines body for PostFiles for multipart/form-data ContentType.
type PostFilesMultipartRequestBody PostFilesMultipartBody

// ServerInterface represents all server handlers.
type ServerInterface interface {
	// Download a file
	// (GET /files)
	GetFiles(w http.ResponseWriter, r *http.Request, params GetFilesParams)
	// Upload a file and ensure the parent directories exist. If the file exists, it will be overwritten.
	// (POST /files)
	PostFiles(w http.ResponseWriter, r *http.Request, params PostFilesParams)
	// Check the health of the service
	// (GET /health)
	GetHealth(w http.ResponseWriter, r *http.Request)
	// Ensure the time and metadata is synced with the host
	// (POST /sync)
	PostSync(w http.ResponseWriter, r *http.Request)
}

// Unimplemented server implementation that returns http.StatusNotImplemented for each endpoint.

type Unimplemented struct{}

// Download a file
// (GET /files)
func (_ Unimplemented) GetFiles(w http.ResponseWriter, r *http.Request, params GetFilesParams) {
	w.WriteHeader(http.StatusNotImplemented)
}

// Upload a file and ensure the parent directories exist. If the file exists, it will be overwritten.
// (POST /files)
func (_ Unimplemented) PostFiles(w http.ResponseWriter, r *http.Request, params PostFilesParams) {
	w.WriteHeader(http.StatusNotImplemented)
}

// Check the health of the service
// (GET /health)
func (_ Unimplemented) GetHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

// Ensure the time and metadata is synced with the host
// (POST /sync)
func (_ Unimplemented) PostSync(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

// ServerInterfaceWrapper converts contexts to parameters.
type ServerInterfaceWrapper struct {
	Handler            ServerInterface
	HandlerMiddlewares []MiddlewareFunc
	ErrorHandlerFunc   func(w http.ResponseWriter, r *http.Request, err error)
}

type MiddlewareFunc func(http.Handler) http.Handler

// GetFiles operation middleware
func (siw *ServerInterfaceWrapper) GetFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var err error

	// Parameter object where we will unmarshal all parameters from the context
	var params GetFilesParams

	// ------------- Optional query parameter "path" -------------

	err = runtime.BindQueryParameter("form", true, false, "path", r.URL.Query(), &params.Path)
	if err != nil {
		siw.ErrorHandlerFunc(w, r, &InvalidParamFormatError{ParamName: "path", Err: err})
		return
	}

	// ------------- Required query parameter "username" -------------

	if paramValue := r.URL.Query().Get("username"); paramValue != "" {

	} else {
		siw.ErrorHandlerFunc(w, r, &RequiredParamError{ParamName: "username"})
		return
	}

	err = runtime.BindQueryParameter("form", true, true, "username", r.URL.Query(), &params.Username)
	if err != nil {
		siw.ErrorHandlerFunc(w, r, &InvalidParamFormatError{ParamName: "username", Err: err})
		return
	}

	handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siw.Handler.GetFiles(w, r, params)
	}))

	for _, middleware := range siw.HandlerMiddlewares {
		handler = middleware(handler)
	}

	handler.ServeHTTP(w, r.WithContext(ctx))
}

// PostFiles operation middleware
func (siw *ServerInterfaceWrapper) PostFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var err error

	// Parameter object where we will unmarshal all parameters from the context
	var params PostFilesParams

	// ------------- Optional query parameter "path" -------------

	err = runtime.BindQueryParameter("form", true, false, "path", r.URL.Query(), &params.Path)
	if err != nil {
		siw.ErrorHandlerFunc(w, r, &InvalidParamFormatError{ParamName: "path", Err: err})
		return
	}

	// ------------- Required query parameter "username" -------------

	if paramValue := r.URL.Query().Get("username"); paramValue != "" {

	} else {
		siw.ErrorHandlerFunc(w, r, &RequiredParamError{ParamName: "username"})
		return
	}

	err = runtime.BindQueryParameter("form", true, true, "username", r.URL.Query(), &params.Username)
	if err != nil {
		siw.ErrorHandlerFunc(w, r, &InvalidParamFormatError{ParamName: "username", Err: err})
		return
	}

	handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siw.Handler.PostFiles(w, r, params)
	}))

	for _, middleware := range siw.HandlerMiddlewares {
		handler = middleware(handler)
	}

	handler.ServeHTTP(w, r.WithContext(ctx))
}

// GetHealth operation middleware
func (siw *ServerInterfaceWrapper) GetHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siw.Handler.GetHealth(w, r)
	}))

	for _, middleware := range siw.HandlerMiddlewares {
		handler = middleware(handler)
	}

	handler.ServeHTTP(w, r.WithContext(ctx))
}

// PostSync operation middleware
func (siw *ServerInterfaceWrapper) PostSync(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siw.Handler.PostSync(w, r)
	}))

	for _, middleware := range siw.HandlerMiddlewares {
		handler = middleware(handler)
	}

	handler.ServeHTTP(w, r.WithContext(ctx))
}

type UnescapedCookieParamError struct {
	ParamName string
	Err       error
}

func (e *UnescapedCookieParamError) Error() string {
	return fmt.Sprintf("error unescaping cookie parameter '%s'", e.ParamName)
}

func (e *UnescapedCookieParamError) Unwrap() error {
	return e.Err
}

type UnmarshalingParamError struct {
	ParamName string
	Err       error
}

func (e *UnmarshalingParamError) Error() string {
	return fmt.Sprintf("Error unmarshaling parameter %s as JSON: %s", e.ParamName, e.Err.Error())
}

func (e *UnmarshalingParamError) Unwrap() error {
	return e.Err
}

type RequiredParamError struct {
	ParamName string
}

func (e *RequiredParamError) Error() string {
	return fmt.Sprintf("Query argument %s is required, but not found", e.ParamName)
}

type RequiredHeaderError struct {
	ParamName string
	Err       error
}

func (e *RequiredHeaderError) Error() string {
	return fmt.Sprintf("Header parameter %s is required, but not found", e.ParamName)
}

func (e *RequiredHeaderError) Unwrap() error {
	return e.Err
}

type InvalidParamFormatError struct {
	ParamName string
	Err       error
}

func (e *InvalidParamFormatError) Error() string {
	return fmt.Sprintf("Invalid format for parameter %s: %s", e.ParamName, e.Err.Error())
}

func (e *InvalidParamFormatError) Unwrap() error {
	return e.Err
}

type TooManyValuesForParamError struct {
	ParamName string
	Count     int
}

func (e *TooManyValuesForParamError) Error() string {
	return fmt.Sprintf("Expected one value for %s, got %d", e.ParamName, e.Count)
}

// Handler creates http.Handler with routing matching OpenAPI spec.
func Handler(si ServerInterface) http.Handler {
	return HandlerWithOptions(si, ChiServerOptions{})
}

type ChiServerOptions struct {
	BaseURL          string
	BaseRouter       chi.Router
	Middlewares      []MiddlewareFunc
	ErrorHandlerFunc func(w http.ResponseWriter, r *http.Request, err error)
}

// HandlerFromMux creates http.Handler with routing matching OpenAPI spec based on the provided mux.
func HandlerFromMux(si ServerInterface, r chi.Router) http.Handler {
	return HandlerWithOptions(si, ChiServerOptions{
		BaseRouter: r,
	})
}

func HandlerFromMuxWithBaseURL(si ServerInterface, r chi.Router, baseURL string) http.Handler {
	return HandlerWithOptions(si, ChiServerOptions{
		BaseURL:    baseURL,
		BaseRouter: r,
	})
}

// HandlerWithOptions creates http.Handler with additional options
func HandlerWithOptions(si ServerInterface, options ChiServerOptions) http.Handler {
	r := options.BaseRouter

	if r == nil {
		r = chi.NewRouter()
	}
	if options.ErrorHandlerFunc == nil {
		options.ErrorHandlerFunc = func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
	}
	wrapper := ServerInterfaceWrapper{
		Handler:            si,
		HandlerMiddlewares: options.Middlewares,
		ErrorHandlerFunc:   options.ErrorHandlerFunc,
	}

	r.Group(func(r chi.Router) {
		r.Get(options.BaseURL+"/files", wrapper.GetFiles)
	})
	r.Group(func(r chi.Router) {
		r.Post(options.BaseURL+"/files", wrapper.PostFiles)
	})
	r.Group(func(r chi.Router) {
		r.Get(options.BaseURL+"/health", wrapper.GetHealth)
	})
	r.Group(func(r chi.Router) {
		r.Post(options.BaseURL+"/sync", wrapper.PostSync)
	})

	return r
}
