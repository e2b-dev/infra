package handler

import (
	"context"
	"errors"
	"syscall"

	"connectrpc.com/connect"
)

// StartErrorCode maps a spawn failure to the Connect code the client should
// see. Resource exhaustion is transient and retryable, so it maps to
// CodeResourceExhausted, never CodeInvalidArgument, which marks the request
// permanently bad. Anything unrecognized stays CodeInvalidArgument.
func StartErrorCode(err error) connect.Code {
	switch {
	// EAGAIN: fork hit RLIMIT_NPROC / pids.max; EMFILE/ENFILE: fd limits while
	// wiring the child's pipes; ENOSPC: out of ptys (/dev/ptmx, kernel.pty.max).
	case errors.Is(err, syscall.EAGAIN),
		errors.Is(err, syscall.ENOMEM),
		errors.Is(err, syscall.EMFILE),
		errors.Is(err, syscall.ENFILE),
		errors.Is(err, syscall.ENOSPC):
		return connect.CodeResourceExhausted
	// exec.CommandContext fails the spawn with the context's error if the request timed out pre-fork.
	case errors.Is(err, context.DeadlineExceeded):
		return connect.CodeDeadlineExceeded
	case errors.Is(err, context.Canceled):
		return connect.CodeCanceled
	default:
		return connect.CodeInvalidArgument
	}
}
