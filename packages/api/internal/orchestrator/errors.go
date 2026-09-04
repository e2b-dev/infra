package orchestrator

import "errors"

var (
	ErrSandboxNotFound        = errors.New("sandbox not found")
	ErrAccessForbidden        = errors.New("access forbidden")
	ErrSandboxOperationFailed = errors.New("sandbox operation failed")
	// ErrRefusedRouteLost: the node refused the pause retryably but the edge
	// could not put the sandbox's route back, so the sandbox cannot be kept.
	ErrRefusedRouteLost = errors.New("pause refused by the node and the edge could not restore its route")
)
