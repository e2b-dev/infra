package server

import (
	"context"
	"fmt"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/types/known/emptypb"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build/writer"
)

func (s *serverStore) TemplateCreate(ctx context.Context, templateRequest *template_manager.TemplateCreateRequest) (*emptypb.Empty, error) {
	childCtx, childSpan := s.tracer.Start(context.Background(), "template-create")
	defer childSpan.End()

	config := templateRequest.Template
	childSpan.SetAttributes(
		attribute.String("env.id", config.TemplateID),
		attribute.String("env.build.id", config.BuildID),
		attribute.String("env.kernel.version", config.KernelVersion),
		attribute.String("env.firecracker.version", config.FirecrackerVersion),
		attribute.String("env.start_cmd", config.StartCommand),
		attribute.Int64("env.memory_mb", int64(config.MemoryMB)),
		attribute.Int64("env.vcpu_count", int64(config.VCpuCount)),
		attribute.Bool("env.huge_pages", config.HugePages),
	)

	logsWriter := writer.New(
		s.buildLogger.
			With(zap.Field{Type: zapcore.StringType, Key: "envID", String: config.TemplateID}).
			With(zap.Field{Type: zapcore.StringType, Key: "buildID", String: config.BuildID}),
	)

	template := &build.Env{
		TemplateFiles: storage.NewTemplateFiles(
			config.TemplateID,
			config.BuildID,
			config.KernelVersion,
			config.FirecrackerVersion,
			config.HugePages,
		),
		VCpuCount:       int64(config.VCpuCount),
		MemoryMB:        int64(config.MemoryMB),
		StartCmd:        config.StartCommand,
		DiskSizeMB:      int64(config.DiskSizeMB),
		BuildLogsWriter: logsWriter,
	}

	err := s.buildCache.Create(config.BuildID, config.TemplateID)
	if err != nil {
		return nil, fmt.Errorf("error while creating build cache: %w", err)
	}


	go func() {

		err = s.builder.Builder(childCtx, template, config.TemplateID, config.BuildID)
		if err != nil {
			s.logger.Error("Error while building template", zap.Error(err))
			telemetry.ReportEvent(childCtx, "Environment built failed")
		}

		cacheIn, cacheErr := s.buildCache.Get(config.BuildID)
		if cacheErr != nil {
			zap.L().Error("template creation cache fetch failed", zap.Error(cacheErr))
		}

		telemetry.ReportEvent(childCtx, "Environment built")
	}()

	cacheIn, cacheErr := s.buildCache.Get(config.BuildID)
	if cacheErr != nil {
		zap.L().Error("template creation cache fetch failed", zap.Error(cacheErr))
		return nil, fmt.Errorf("error while getting build info, maybe already expired")
	}


	return nil, nil
}
