package server

import (
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"go.uber.org/zap"
)

func (s *serverStore) TemplateBuildStatus(ctx context.Context, in *template_manager.TemplateStatusRequest) (*template_manager.TemplateBuildStatusResponse, error) {
	ctx, ctxSpan := s.tracer.Start(ctx, "template-build-status-request")
	defer ctxSpan.End()

	logger := s.logger.With(zap.String("buildID", in.BuildID), zap.String("envID", in.TemplateID))
	logger.Info("Template build status request")

	buildInfo, err := s.buildCache.Get(in.BuildID)
	if err != nil {
		return nil, fmt.Errorf("error while getting build info, maybe already expired")
	}

	if buildInfo.IsFailed() {
		logger.Error("Template build failed")
		return nil, fmt.Errorf("template build failed")
	}

	return &template_manager.TemplateBuildStatusResponse{
		Status:   buildInfo.GetStatus(),
		Metadata: buildInfo.GetMetadata(),
	}, nil
}
