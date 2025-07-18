package server

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *ServerStore) TemplateCreate(ctx context.Context, templateRequest *templatemanager.TemplateCreateRequest) (*emptypb.Empty, error) {
	_, childSpan := s.tracer.Start(ctx, "template-create")
	defer childSpan.End()

	cfg := templateRequest.Template
	childSpan.SetAttributes(
		telemetry.WithTemplateID(cfg.TemplateID),
		telemetry.WithBuildID(cfg.BuildID),
		attribute.String("env.kernel.version", cfg.KernelVersion),
		attribute.String("env.firecracker.version", cfg.FirecrackerVersion),
		attribute.String("env.start_cmd", cfg.StartCommand),
		attribute.Int64("env.memory_mb", int64(cfg.MemoryMB)),
		attribute.Int64("env.vcpu_count", int64(cfg.VCpuCount)),
		attribute.Bool("env.huge_pages", cfg.HugePages),
	)

	if s.healthStatus == templatemanager.HealthState_Draining {
		s.logger.Error("Requesting template creation while server is draining is not possible", logger.WithTemplateID(cfg.TemplateID))
		return nil, fmt.Errorf("server is draining")
	}

	metadata := storage.TemplateFiles{
		TemplateID:         cfg.TemplateID,
		BuildID:            cfg.BuildID,
		KernelVersion:      cfg.KernelVersion,
		FirecrackerVersion: cfg.FirecrackerVersion,
	}
	template := config.TemplateConfig{
		VCpuCount:  int64(cfg.VCpuCount),
		MemoryMB:   int64(cfg.MemoryMB),
		StartCmd:   cfg.StartCommand,
		ReadyCmd:   cfg.ReadyCommand,
		DiskSizeMB: int64(cfg.DiskSizeMB),
		HugePages:  cfg.HugePages,
		FromImage:  cfg.FromImage,
		Force:      cfg.Force,
		Steps:      cfg.Steps,
	}

	logs := cache.NewSafeBuffer()
	buildInfo, err := s.buildCache.Create(metadata.BuildID, logs)
	if err != nil {
		return nil, fmt.Errorf("error while creating build cache: %w", err)
	}

	// Add new core that will log all messages using logger (zap.Logger) to the logs buffer too
	encoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	bufferCore := zapcore.NewCore(encoder, logs, zapcore.DebugLevel)
	core := zapcore.NewTee(bufferCore, s.buildLogger.Core().
		With([]zap.Field{
			{Type: zapcore.StringType, Key: "envID", String: metadata.TemplateID},
			{Type: zapcore.StringType, Key: "buildID", String: metadata.BuildID},
		}),
	)
	logger := zap.New(core)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		buildContext, cancelBuildContext := context.WithCancel(context.Background())
		defer cancelBuildContext()

		buildContext, buildSpan := s.tracer.Start(
			trace.ContextWithSpanContext(buildContext, childSpan.SpanContext()),
			"template-background-build",
		)
		defer buildSpan.End()

		// Watch for build cancellation requests
		go func() {
			select {
			case <-buildContext.Done():
				return
			case <-buildInfo.Cancel.Done:
				_, err := buildInfo.Cancel.Result()
				if err != nil {
					reason := err.Error()
					buildInfo.SetFail(&reason)
					cancelBuildContext()
				}
				return
			}
		}()

		res, err := s.builder.Build(buildContext, metadata, template, logger)
		_ = logger.Sync()
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error while building template", err)

			reason := err.Error()
			buildInfo.SetFail(&reason)
		} else {
			buildInfo.SetSuccess(&templatemanager.TemplateBuildMetadata{
				RootfsSizeKey:  int32(res.RootfsSizeMB),
				EnvdVersionKey: res.EnvdVersion,
			})
			telemetry.ReportEvent(buildContext, "Environment built")
		}
	}()

	return nil, nil
}
