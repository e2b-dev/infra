package server

import (
	"context"
	"fmt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/types/known/emptypb"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build/writer"
)

func (s *ServerStore) TemplateCreate(ctx context.Context, templateRequest *template_manager.TemplateCreateRequest) (*emptypb.Empty, error) {
	_, childSpan := s.tracer.Start(ctx, "template-create")
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

	if s.healthStatus == template_manager.HealthState_Draining {
		s.logger.Error("Requesting template creation while server is draining is not possible", zap.String("envID", config.TemplateID))
		return nil, fmt.Errorf("server is draining")
	}

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

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		buildContext, buildSpan := s.tracer.Start(
			trace.ContextWithSpanContext(context.Background(), childSpan.SpanContext()),
			"template-background-build",
		)
		defer buildSpan.End()

		err = s.builder.Build(buildContext, template, config.TemplateID, config.BuildID)
		if err != nil {
			cacheErr := s.buildCache.SetFailed(config.TemplateID, config.BuildID)
			if cacheErr != nil {
				s.logger.Error("Error during failing template build in cache", zap.Error(err))
			}

			s.logger.Error("Error while building template", zap.Error(err))
			telemetry.ReportEvent(buildContext, "Environment built failed")
			return
		}

		telemetry.ReportEvent(buildContext, "Environment built")
	}()

	return nil, nil
}
