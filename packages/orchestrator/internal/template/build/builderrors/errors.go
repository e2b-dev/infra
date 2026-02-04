package builderrors

import (
	"context"
	"errors"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
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

// WrapContextAsUserError wraps context.Canceled as a user error if no user error already exists.
// This ensures that user-initiated cancellations are tracked as user errors in metrics.
func WrapContextAsUserError(err error) error {
	if err == nil {
		return nil
	}

	// If it's already a user error, return as-is
	if IsUserError(err) {
		return err
	}

	// If it's a canceled context, wrap it as a user error
	if errors.Is(err, context.Canceled) {
		return phases.NewPhaseBuildError(phases.PhaseMeta{}, ErrCanceled)
	}

	// If it's a timeout context, wrap it as a user error
	if errors.Is(err, context.DeadlineExceeded) {
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
