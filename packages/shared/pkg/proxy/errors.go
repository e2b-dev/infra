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

// SandboxStillTransitioningError means the sandbox is pausing and a retry
// in a moment will find it. Unavailable renders it as 503 instead of 409:
// the SDK reads a 409 on the envd port as "already exists".
type SandboxStillTransitioningError struct {
	SandboxId   string
	Unavailable bool
}

func NewErrSandboxStillTransitioning(sandboxId string) *SandboxStillTransitioningError {
	return &SandboxStillTransitioningError{
		SandboxId: sandboxId,
	}
}

func NewErrSandboxStillTransitioningUnavailable(sandboxId string) *SandboxStillTransitioningError {
	return &SandboxStillTransitioningError{
		SandboxId:   sandboxId,
		Unavailable: true,
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

// SandboxCapacityError means the resume could not be carried out right now
// (no node could take the sandbox, the control plane was unreachable, or the
// proxy had no room to wait); unlike SandboxResourceExhaustedError it is not
// the team's limit and clears on its own. Permanent marks the exception: a
// placement no retry helps (a feature no orchestrator in the cluster
// supports), answered without a retry hint. Message is the control plane's
// own wording, kept for the log; the page never shows it.
type SandboxCapacityError struct {
	SandboxId string
	Message   string
	Permanent bool
	// ErrorCode is the API's code for a permanent refusal, shown to clients.
	ErrorCode string
	// StatusCode is the HTTP status a permanent refusal answers with: the
	// one the API returned, so a refusal it answered as a bad request is
	// not a 503 the client retries; zero means 503.
	StatusCode int
	// Shed marks a request turned away because the proxy had no room to
	// wait; its answer carries no Retry-After, so clients that honour the
	// header do not come back in one wave while the lot is still full, and
	// an error_code, so it is told from the capacity answer without one.
	Shed bool
}

func NewErrSandboxCapacity(sandboxId string, message string) *SandboxCapacityError {
	return &SandboxCapacityError{
		SandboxId: sandboxId,
		Message:   message,
	}
}

func NewErrSandboxShed(sandboxId string, message string) *SandboxCapacityError {
	return &SandboxCapacityError{
		SandboxId: sandboxId,
		Message:   message,
		Shed:      true,
	}
}

func NewErrSandboxPlacementRefused(sandboxId string, message string, errorCode string, statusCode int) *SandboxCapacityError {
	return &SandboxCapacityError{
		SandboxId:  sandboxId,
		Message:    message,
		Permanent:  true,
		ErrorCode:  errorCode,
		StatusCode: statusCode,
	}
}

func (e SandboxCapacityError) Error() string {
	if e.Permanent {
		return "sandbox cannot be placed on this cluster"
	}

	return "sandbox cannot be resumed right now"
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
