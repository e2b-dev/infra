package builderrors

import (
	"errors"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type TemplateBuildError struct {
	Message string
	Err     error
}

func NewTemplateBuildError(message string, err error) *TemplateBuildError {
	return &TemplateBuildError{
		Message: message,
		Err:     err,
	}
}

func (e *TemplateBuildError) Error() string {
	return fmt.Sprintf("%s: %v", e.Message, e.Unwrap())
}

func (e *TemplateBuildError) Unwrap() error {
	return e.Err
}

func UnwrapUserError(err error) *template_manager.TemplateBuildStatusReason {
	var templateBuildError *TemplateBuildError
	if errors.As(err, &templateBuildError) {
		phaseBuildError := phases.UnwrapPhaseBuildError(templateBuildError.Err)
		if phaseBuildError != nil {
			return &template_manager.TemplateBuildStatusReason{
				Message: phaseBuildError.Message,
				Step:    phaseBuildError.Step,
			}
		}

		return &template_manager.TemplateBuildStatusReason{
			Message: templateBuildError.Message,
		}
	}
	return &template_manager.TemplateBuildStatusReason{
		Message: err.Error(),
	}
}
