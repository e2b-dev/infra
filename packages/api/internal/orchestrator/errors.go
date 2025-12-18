package orchestrator

import "errors"

var (
	ErrSandboxNotFound        = errors.New("sandbox not found")
	ErrSandboxOperationFailed = errors.New("sandbox operation failed")
)
