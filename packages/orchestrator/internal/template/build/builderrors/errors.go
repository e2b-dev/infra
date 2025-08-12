package builderrors

import (
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func UnwrapUserError(err error) *template_manager.TemplateBuildStatusReason {
	phaseBuildError := phases.UnwrapPhaseBuildError(err)
	if phaseBuildError != nil {
		return &template_manager.TemplateBuildStatusReason{
			Message: phaseBuildError.Error(),
			Step:    phaseBuildError.Step,
		}
	}

	return &template_manager.TemplateBuildStatusReason{
		Message: err.Error(),
	}
}
