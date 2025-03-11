package server

import (
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"go.uber.org/zap"
	"time"
)

const statusTimeout = time.Minute * 5

func (s *serverStore) TemplateBuildStatus(in *template_manager.TemplateStatusRequest, stream template_manager.TemplateService_TemplateBuildStatusServer) error {
	ctx, cancel := context.WithTimeout(stream.Context(), statusTimeout)
	defer cancel()

	ctx, ctxSpan := s.tracer.Start(ctx, "template-build-status-request")
	defer ctxSpan.End()

	logger := s.logger.With(zap.String("buildID", in.BuildID), zap.String("envID", in.TemplateID))

	// stream updated status until build is done or timeout will kick in
	for {
		buildInfo, err := s.buildCache.Get(in.BuildID)
		if err != nil {
			return fmt.Errorf("error while getting build info, maybe already expired")
		}

		var metadata *template_manager.TemplateBuildMetadata = nil
		if !buildInfo.IsRunning() {
			metadata = &template_manager.TemplateBuildMetadata{
				RootfsSizeKey:  buildInfo.GetRootFsSizeKey(),
				EnvdVersionKey: buildInfo.GetEnvdVersionKey(),
			}
		}

		if buildInfo.IsFailed() {
			logger.Error("Template build failed")
			return fmt.Errorf("template build failed")
		}

		err = stream.Send(
			&template_manager.TemplateBuildStatusResponse{
				Done:     !buildInfo.IsRunning(),
				Failed:   buildInfo.IsFailed(),
				Metadata: metadata,
			},
		)

		if err != nil {
			logger.Error("Error while sending status stream back to API", zap.Error(err))
			telemetry.ReportError(ctx, err)
			return err
		}

		if !buildInfo.IsRunning() {
			logger.Info("Template build finished")
			return nil
		}

		time.Sleep(time.Second * 5)
	}

	return nil
}
