package proxy

import (
	"errors"
)

var (
	ErrInvalidHost      = errors.New("invalid url host")
	ErrInvalidSandboxID = errors.New("invalid sandbox ID")
)

type InvalidSandboxPortError struct {
	Port    string
	wrapped error
}

func (e InvalidSandboxPortError) Error() string {
	return "invalid sandbox port"
}

func (e InvalidSandboxPortError) Unwrap() error {
	return e.wrapped
}

type SandboxNotFoundError struct {
	SandboxId string
}

func NewErrSandboxNotFound(sandboxId string) *SandboxNotFoundError {
	return &SandboxNotFoundError{
		SandboxId: sandboxId,
	}
}

func (e SandboxNotFoundError) Error() string {
	return "sandbox not found"
}

type SandboxResumePermissionDeniedError struct {
	SandboxId string
}

func NewErrSandboxResumePermissionDenied(sandboxId string) *SandboxResumePermissionDeniedError {
	return &SandboxResumePermissionDeniedError{
		SandboxId: sandboxId,
	}
}

func (e SandboxResumePermissionDeniedError) Error() string {
	return "sandbox resume permission denied"
}

type SandboxStillTransitioningError struct {
	SandboxId string
}

func NewErrSandboxStillTransitioning(sandboxId string) *SandboxStillTransitioningError {
	return &SandboxStillTransitioningError{
		SandboxId: sandboxId,
	}
}

func (e SandboxStillTransitioningError) Error() string {
	return "sandbox is still transitioning"
}

type MissingTrafficAccessTokenError struct {
	SandboxId string
	Header    string
}

func (e MissingTrafficAccessTokenError) Error() string {
	return "missing traffic access token"
}

func NewErrMissingTrafficAccessToken(sandboxId, header string) *MissingTrafficAccessTokenError {
	return &MissingTrafficAccessTokenError{
		SandboxId: sandboxId,
		Header:    header,
	}
}

type InvalidTrafficAccessTokenError struct {
	SandboxId string
	Header    string
}

func (e InvalidTrafficAccessTokenError) Error() string {
	return "invalid traffic access token"
}

func NewErrInvalidTrafficAccessToken(sandboxId string, header string) *InvalidTrafficAccessTokenError {
	return &InvalidTrafficAccessTokenError{
		SandboxId: sandboxId,
		Header:    header,
	}
}

// InternalRouteError means the request addressed a route reserved for the
// control plane, which nothing reaching the proxy is entitled to call. It is
// named for what happened so the logs say so; for why the response is a 404 that
// still explains itself, see template.NewInternalRouteError.
type InternalRouteError struct {
	SandboxId string
	Path      string
}

func NewErrInternalRoute(sandboxId, path string) *InternalRouteError {
	return &InternalRouteError{
		SandboxId: sandboxId,
		Path:      path,
	}
}

func (e InternalRouteError) Error() string {
	return "internal route is not reachable through the proxy"
}

type SandboxResourceExhaustedError struct {
	SandboxId string
	Message   string
}

func NewErrSandboxResourceExhausted(sandboxId string, message string) *SandboxResourceExhaustedError {
	return &SandboxResourceExhaustedError{
		SandboxId: sandboxId,
		Message:   message,
	}
}

func (e SandboxResourceExhaustedError) Error() string {
	return "sandbox resource exhausted"
}
