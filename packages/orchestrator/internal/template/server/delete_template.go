package server

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *ServerStore) TemplateBuildDelete(ctx context.Context, in *templatemanager.TemplateBuildDeleteRequest) (*emptypb.Empty, error) {
	childCtx, childSpan := s.tracer.Start(ctx, "template-delete-request", trace.WithAttributes(
		telemetry.WithTemplateID(in.TemplateID),
		telemetry.WithBuildID(in.BuildID),
	))
	defer childSpan.End()

	s.wg.Add(1)
	defer s.wg.Done()

	if in.TemplateID == "" || in.BuildID == "" {
		return nil, errors.New("template id and build id are required fields")
	}

	c, err := s.buildCache.Get(in.BuildID)
	if err == nil {
		// Only handle if the build is in the cache
		zap.L().Info("Canceling running template build", logger.WithTemplateID(in.TemplateID), logger.WithBuildID(in.TemplateID))
		telemetry.ReportEvent(ctx, "cancel in progress template build")
		c.Cancel()
	}

	err = template.Delete(childCtx, s.tracer, s.artifactsregistry, s.templateStorage, in.TemplateID, in.BuildID)
	if err != nil {
		return nil, err
	}

	return nil, nil
}
