package server

import (
	"context"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (s *serverStore) TemplateBuildStatus(ctx context.Context, in *template_manager.TemplateStatusRequest) (*template_manager.TemplateBuildStatusResponse, error) {
	ctx, ctxSpan := s.tracer.Start(ctx, "template-build-status-request")
	defer ctxSpan.End()

	logger := s.logger.With(zap.String("buildID", in.BuildID), zap.String("envID", in.TemplateID))
	logger.Info("Template build status request")

	buildInfo, err := s.buildCache.Get(in.BuildID)
	if err != nil {
		return nil, errors.WithMessage(errors.WithStack(err), "error while getting build info, maybe already expired")
	}

	return &template_manager.TemplateBuildStatusResponse{
		Status:   buildInfo.GetStatus(),
		Metadata: buildInfo.GetMetadata(),
	}, nil
}
