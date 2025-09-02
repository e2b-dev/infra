package server

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/builderrors"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/oci/auth"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
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

	if s.info.GetStatus() != orchestrator.ServiceInfoStatus_Healthy {
		s.logger.Error("Requesting template creation while server not healthy is not possible", logger.WithTemplateID(cfg.TemplateID))
		return nil, fmt.Errorf("server is draining")
	}

	metadata := storage.TemplateFiles{
		BuildID:            cfg.BuildID,
		KernelVersion:      cfg.KernelVersion,
		FirecrackerVersion: cfg.FirecrackerVersion,
	}

	// default to scope by template ID
	cacheScope := cfg.TemplateID
	if templateRequest.CacheScope != nil {
		cacheScope = *templateRequest.CacheScope
	}

	// Create the auth provider using the factory
	authProvider := auth.NewAuthProvider(cfg.FromImageRegistry)

	template := config.TemplateConfig{
		TemplateID:           cfg.TemplateID,
		CacheScope:           cacheScope,
		VCpuCount:            int64(cfg.VCpuCount),
		MemoryMB:             int64(cfg.MemoryMB),
		StartCmd:             cfg.StartCommand,
		ReadyCmd:             cfg.ReadyCommand,
		DiskSizeMB:           int64(cfg.DiskSizeMB),
		HugePages:            cfg.HugePages,
		FromImage:            cfg.GetFromImage(),
		FromTemplate:         cfg.GetFromTemplate(),
		RegistryAuthProvider: authProvider,
		Force:                cfg.Force,
		Steps:                cfg.Steps,
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
			{Type: zapcore.StringType, Key: "envID", String: cfg.TemplateID},
			{Type: zapcore.StringType, Key: "buildID", String: metadata.BuildID},
		}),
	)
	logger := zap.New(core)

	s.wg.Add(1)
	go func(ctx context.Context) {
		defer s.wg.Done()

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		defer func() {
			if r := recover(); r != nil {
				zap.L().Error("recovered from panic in template build", zap.Any("panic", r))
			}
		}()

		ctx, buildSpan := s.tracer.Start(ctx, "template-background-build")
		defer buildSpan.End()

		// Watch for build cancellation requests
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-buildInfo.Result.Done:
				res, _ := buildInfo.Result.Result()
				if res.Status == templatemanager.TemplateBuildState_Failed {
					cancel()
				}
				return
			}
		}()

		res, err := s.builder.Build(ctx, metadata, template, logger)
		_ = logger.Sync()
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error while building template", err)

			buildInfo.SetFail(builderrors.UnwrapUserError(err))
		} else {
			buildInfo.SetSuccess(&templatemanager.TemplateBuildMetadata{
				RootfsSizeKey:  int32(res.RootfsSizeMB),
				EnvdVersionKey: res.EnvdVersion,
			})
			telemetry.ReportEvent(ctx, "Environment built")
		}
	}(context.WithoutCancel(ctx))

	return nil, nil
}
