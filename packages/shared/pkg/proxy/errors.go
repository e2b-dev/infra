package proxy

import (
	"errors"
)

var (
	ErrInvalidHost               = errors.New("invalid url host")
	ErrInvalidTrafficAccessToken = errors.New("invalid traffic access token")
	ErrMissingTrafficAccessToken = errors.New("missing traffic access token")
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
