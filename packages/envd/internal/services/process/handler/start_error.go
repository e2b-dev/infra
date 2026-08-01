package handler

import (
	"context"
	"errors"
	"syscall"

	"connectrpc.com/connect"
)

// StartErrorCode maps a failure to spawn a process to the Connect code the
// client should see.
//
// A spawn fails for two fundamentally different reasons and they must not
// share a code. A malformed request — unknown binary, non-existent cwd — is
// the caller's fault and is permanent: the identical request will never
// succeed, so CodeInvalidArgument is correct. Running out of process capacity
// is neither. fork/exec returns EAGAIN once the sandbox reaches RLIMIT_NPROC,
// its cgroup pids.max or the kernel's pid_max, and the very same request
// succeeds again as soon as any process in the sandbox exits. Reporting that
// as CodeInvalidArgument tells every SDK the request was bad and must not be
// retried, which is both wrong and undebuggable: the user sees a permanent
// "bad request" for a command that is perfectly valid. CodeResourceExhausted
// names what actually happened and is the code SDK retry policies key on.
//
// The default stays CodeInvalidArgument, so behaviour is unchanged for every
// failure that is not resource exhaustion or a spent request context.
func StartErrorCode(err error) connect.Code {
	switch {
	// EAGAIN: fork(2) hit RLIMIT_NPROC, the cgroup's pids.max or pid_max.
	// ENOMEM: the kernel could not allocate the new task.
	// EMFILE/ENFILE: the per-process or system-wide fd limit was reached while
	// setting up the child's pipes or opening /dev/ptmx.
	// ENOSPC: /dev/ptmx is out of pseudoterminals (kernel.pty.max).
	case errors.Is(err, syscall.EAGAIN),
		errors.Is(err, syscall.ENOMEM),
		errors.Is(err, syscall.EMFILE),
		errors.Is(err, syscall.ENFILE),
		errors.Is(err, syscall.ENOSPC):
		return connect.CodeResourceExhausted
	// exec.CommandContext fails the spawn with the context's error when the
	// request timeout expires before the fork happens.
	case errors.Is(err, context.DeadlineExceeded):
		return connect.CodeDeadlineExceeded
	case errors.Is(err, context.Canceled):
		return connect.CodeCanceled
	default:
		return connect.CodeInvalidArgument
	}
}
