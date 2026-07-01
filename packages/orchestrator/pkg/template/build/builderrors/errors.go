//go:build linux

package builderrors

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// WrapContextAsUserError wraps context.Canceled as a user error if no user error already exists.
// This ensures that user-initiated cancellations are tracked as user errors in metrics.
//
// When the error already contains a PhaseBuildError (e.g. annotated by phases.Run
// with the active phase/step), the phase metadata is extracted and re-wrapped with
// the standardized ErrCanceled/ErrTimeout sentinel so that errors.Is still works.
// The original error chain is preserved via a sanitizedError wrapper that cleans
// up newlines in Error() while keeping Unwrap() intact.
func WrapContextAsUserError(err error) error {
	if err == nil {
		return nil
	}

	// Extract phase metadata if present, so we can re-wrap with the standard sentinel.
	// When err is exactly the PhaseBuildError (not wrapped by an outer error like
	// errors.Join), we unwrap to pbe.Err to avoid duplicating phase/step in the
	// message. When err has outer wrapping context, we keep the full err so that
	// diagnostic details are not lost.
	var phase, step string
	errToWrap := err
	if pbe := phases.UnwrapPhaseBuildError(err); pbe != nil {
		phase = pbe.Phase
		step = pbe.Step
		// Only unwrap when err is the PhaseBuildError itself; if there is
		// outer wrapping (e.g. errors.Join), preserve the full context.
		if err == error(pbe) {
			errToWrap = pbe.Err
		}
		// For non-cancel/timeout user errors, return as-is.
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
	}

	// wrapWithPhase creates a PhaseBuildError preserving any phase/step context
	// extracted above, with the given sentinel wrapping the original error chain.
	wrapWithPhase := func(sentinel error) error {
		return &phases.PhaseBuildError{
			Phase: phase,
			Step:  step,
			Err:   fmt.Errorf("%w: %w", sentinel, sanitizedError{errToWrap}),
		}
	}

	// If it's a canceled context, wrap it as a user error while preserving the
	// original error chain for diagnostics.
	if errors.Is(err, context.Canceled) {
		return wrapWithPhase(ErrCanceled)
	}

	// If it's a timeout context, wrap it as a user error while preserving the
	// original error chain for diagnostics.
	if errors.Is(err, context.DeadlineExceeded) {
		return wrapWithPhase(ErrTimeout)
	}

	return err
}

// sanitizedError wraps an error, replacing newlines in Error() output
// (e.g. from errors.Join) while preserving the original error chain via Unwrap.
type sanitizedError struct {
	err error
}

func (e sanitizedError) Error() string {
	if e.err == nil {
		return ""
	}
	return strings.ReplaceAll(e.err.Error(), "\n", "; ")
}

func (e sanitizedError) Unwrap() error {
	return e.err
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
