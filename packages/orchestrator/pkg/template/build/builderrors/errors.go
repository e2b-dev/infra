//go:build linux

package builderrors

import (
	"context"
	"errors"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/phases"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

const (
	InternalErrorMessage = "An internal error occurred. Please try again or contact support with the build ID."
)

// User errors
var (
	ErrCanceled = errors.New("build was cancelled")
	ErrTimeout  = errors.New("build timed out")
)

// IsUserError returns true if the error is a user error (i.e., a PhaseBuildError).
// User errors are caused by the user's configuration and should be shown to them.
func IsUserError(err error) bool {
	return phases.UnwrapPhaseBuildError(err) != nil
}

// WrapContextAsUserError wraps context errors as user errors only when the build-level
// context itself was canceled or timed out. This prevents internal child-context
// cancellations (e.g., envd init timeout) from being misclassified as user cancellations.
func WrapContextAsUserError(buildCtx context.Context, err error) error {
	if err == nil {
		return nil
	}

	// If it's already a user error, return as-is
	if IsUserError(err) {
		return err
	}

	// Only classify as user cancellation/timeout if the build-level context was actually done.
	// Internal child-context cancellations should not be treated as user errors.
	if buildCtx.Err() == nil {
		return err
	}

	// If the build context was canceled AND the error chain contains context.Canceled,
	// wrap it as a user cancellation. The double check ensures we don't discard
	// unrelated errors (e.g., disk errors) that happen to coincide with context cancellation.
	if buildCtx.Err() == context.Canceled && errors.Is(err, context.Canceled) {
		return phases.NewPhaseBuildError(phases.PhaseMeta{}, ErrCanceled)
	}

	// If the build context deadline was exceeded AND the error chain contains
	// context.DeadlineExceeded, wrap it as a user timeout.
	if buildCtx.Err() == context.DeadlineExceeded && errors.Is(err, context.DeadlineExceeded) {
		return phases.NewPhaseBuildError(phases.PhaseMeta{}, ErrTimeout)
	}

	return err
}

func UnwrapUserError(err error) *template_manager.TemplateBuildStatusReason {
	phaseBuildError := phases.UnwrapPhaseBuildError(err)
	if phaseBuildError != nil {
		return &template_manager.TemplateBuildStatusReason{
			Message: phaseBuildError.Error(),
			Step:    &phaseBuildError.Step,
		}
	}

	return &template_manager.TemplateBuildStatusReason{
		Message: InternalErrorMessage,
	}
}
