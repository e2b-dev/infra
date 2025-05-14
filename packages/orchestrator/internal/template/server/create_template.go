package server

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *ServerStore) TemplateCreate(ctx context.Context, templateRequest *templatemanager.TemplateCreateRequest) (*emptypb.Empty, error) {
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

	if s.healthStatus == templatemanager.HealthState_Draining {
		s.logger.Error("Requesting template creation while server is draining is not possible", zap.String("envID", config.TemplateID))
		return nil, fmt.Errorf("server is draining")
	}

	logsWriter := writer.New(
		s.buildLogger.
			With(zap.Field{Type: zapcore.StringType, Key: "envID", String: config.TemplateID}).
			With(zap.Field{Type: zapcore.StringType, Key: "buildID", String: config.BuildID}),
	)

	template := &build.TemplateConfig{
		TemplateFiles: storage.NewTemplateFiles(
			config.TemplateID,
			config.BuildID,
			config.KernelVersion,
			config.FirecrackerVersion,
		),
		VCpuCount:       int64(config.VCpuCount),
		MemoryMB:        int64(config.MemoryMB),
		StartCmd:        config.StartCommand,
		DiskSizeMB:      int64(config.DiskSizeMB),
		BuildLogsWriter: logsWriter,
		HugePages:       config.HugePages,
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

		res, err := s.builder.Build(buildContext, template)
		// Wait for the CLI to load all the logs
		// This is a temporary ~fix for the CLI to load most of the logs before finishing the template build
		// Ideally we should wait in the CLI for the last log message
		time.Sleep(5 * time.Second)
		if err != nil {
			s.reportBuildFailed(buildContext, s.buildLogger, template, err)
			return
		}

		buildMetadata := &templatemanager.TemplateBuildMetadata{RootfsSizeKey: int32(template.RootfsSizeMB()), EnvdVersionKey: res.EnvdVersion}
		err = s.buildCache.SetSucceeded(template.TemplateId, template.BuildId, buildMetadata)
		if err != nil {
			s.reportBuildFailed(buildContext, s.buildLogger, template, fmt.Errorf("error while setting build state to succeeded: %w", err))
			return
		}

		telemetry.ReportEvent(buildContext, "Environment built")
	}()

	return nil, nil
}

func (s *ServerStore) reportBuildFailed(ctx context.Context, logger *zap.Logger, config *build.TemplateConfig, err error) {
	telemetry.ReportCriticalError(ctx, err)
	cacheErr := s.buildCache.SetFailed(config.TemplateId, config.BuildId)
	if cacheErr != nil {
		s.logger.Error("Error while setting build state to failed", zap.Error(err))
	}

	logger.Error("Error while building template", zap.Error(err))
	telemetry.ReportEvent(ctx, "Environment built failed")
}
