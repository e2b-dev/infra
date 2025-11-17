package ioc

import (
	"context"
	"fmt"
	"log"
	"slices"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	tmplserver "github.com/e2b-dev/infra/packages/orchestrator/internal/template/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func NewTemplateManagerModule(config cfg.Config) fx.Option {
	return If(
		"template-manager",
		slices.Contains(config.Services, string(cfg.TemplateManager)),
		fx.Provide(
			NewTemplateManager,
		),
	)
}

func NewTemplateManager(
	lc fx.Lifecycle,
	config cfg.Config,
	sandboxFactory *sandbox.Factory,
	sandboxProxy *proxy.SandboxProxy,
	sandboxes *sandbox.Map,
	featureFlags *featureflags.Client,
	templateCache *template.Cache,
	persistence storage.StorageProvider,
	limiter *limit.Limiter,
	serviceInfo *service.ServiceInfo,
	globalLogger *zap.Logger,
	tel *telemetry.Client,
) (*tmplserver.ServerStore, error) {
	// template manager sandbox logger
	tmplSbxLoggerExternal := sbxlogger.NewLogger(
		context.Background(),
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      constants.ServiceNameTemplate,
			IsInternal:       false,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			err := tmplSbxLoggerExternal.Sync()
			if logger.IsSyncError(err) {
				log.Printf("error while shutting down template manager sandbox logger: %v", err)

				return err
			}

			return nil
		},
	})

	tmpl, err := tmplserver.New(
		context.Background(),
		config,
		featureFlags,
		tel.MeterProvider,
		globalLogger,
		tmplSbxLoggerExternal,
		sandboxFactory,
		sandboxProxy,
		sandboxes,
		templateCache,
		persistence,
		limiter,
		serviceInfo,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create template manager: %w", err)
	}

	globalLogger.Info("Registered gRPC service", zap.String("service", "template_manager.TemplateService"))

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			globalLogger.Info("Shutting down template manager")

			return tmpl.Close(ctx)
		},
	})

	return tmpl, nil
}
