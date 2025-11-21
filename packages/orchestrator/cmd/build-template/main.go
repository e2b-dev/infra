package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
)

const (
	baseImage = "e2bdev/base:latest"

	proxyPort = 5007
)

func main() {
	ctx := context.Background()

	templateID := flag.String("template", "", "template id")
	buildID := flag.String("build", "", "build id")
	kernelVersion := flag.String("kernel", "", "kernel version")
	fcVersion := flag.String("firecracker", "", "firecracker version")
	flag.Parse()

	builderConfig, err := cfg.ParseBuilder()
	if err != nil {
		log.Fatalf("error parsing builder config: %v", err)
	}

	networkConfig, err := network.ParseConfig()
	if err != nil {
		log.Fatalf("error parsing network config: %v", err)
	}

	err = buildTemplate(ctx, *kernelVersion, *fcVersion, *templateID, *buildID, builderConfig, networkConfig)
	if err != nil {
		log.Fatalf("error building template: %v", err)
	}
}

func buildTemplate(
	parentCtx context.Context,
	kernelVersion,
	fcVersion,
	templateID,
	buildID string,
	builderConfig cfg.BuilderConfig,
	networkConfig network.Config,
) error {
	ctx, cancel := context.WithTimeout(parentCtx, time.Minute*5)
	defer cancel()

	clientID := "build-template-cmd"
	logger, err := l.NewLogger(ctx, l.LoggerConfig{
		ServiceName:   clientID,
		IsInternal:    true,
		IsDebug:       true,
		EnableConsole: true,
	})
	if err != nil {
		return fmt.Errorf("could not create logger: %w", err)
	}
	zap.ReplaceGlobals(logger)
	sbxlogger.SetSandboxLoggerExternal(logger)
	sbxlogger.SetSandboxLoggerInternal(logger)

	logger.Info("building template", l.WithTemplateID(templateID), l.WithBuildID(buildID))

	// The sandbox map is shared between the server and the proxy
	// to propagate information about sandbox routing.
	sandboxes := sandbox.NewSandboxesMap()

	sandboxProxy, err := proxy.NewSandboxProxy(noop.MeterProvider{}, proxyPort, sandboxes)
	if err != nil {
		logger.Fatal("failed to create sandbox proxy", zap.Error(err))
	}
	go func() {
		err := sandboxProxy.Start(parentCtx)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("failed to start sandbox proxy", zap.Error(err))
		}
	}()
	defer func() {
		err := sandboxProxy.Close(parentCtx)
		if err != nil {
			logger.Error("error closing sandbox proxy", zap.Error(err))
		}
	}()

	persistenceTemplate, err := storage.GetTemplateStorageProvider(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not create storage provider: %w", err)
	}

	persistenceBuild, err := storage.GetBuildCacheStorageProvider(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not create storage provider: %w", err)
	}

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return fmt.Errorf("could not create device pool: %w", err)
	}
	go func() {
		devicePool.Populate(ctx)
		logger.Info("device pool done populating")
	}()
	defer func() {
		if err := devicePool.Close(parentCtx); err != nil {
			logger.Error("error closing device pool", zap.Error(err))
		}
	}()

	slotStorage, err := network.NewStorageLocal(networkConfig)
	if err != nil {
		return fmt.Errorf("could not create network pool: %w", err)
	}
	networkPool := network.NewPool(8, 8, slotStorage, networkConfig)
	go func() {
		networkPool.Populate(ctx)
		logger.Info("network pool done populating")
	}()
	defer func() {
		err := networkPool.Close(parentCtx)
		if err != nil {
			logger.Error("error closing network pool", zap.Error(err))
		}
	}()

	artifactRegistry, err := artifactsregistry.GetArtifactsRegistryProvider(ctx)
	if err != nil {
		return fmt.Errorf("error getting artifacts registry provider: %w", err)
	}

	dockerhubRepository, err := dockerhub.GetRemoteRepository(ctx)
	if err != nil {
		return fmt.Errorf("error getting dockerhub repository: %w", err)
	}
	defer func() {
		err := dockerhubRepository.Close()
		if err != nil {
			logger.Error("error closing dockerhub repository", zap.Error(err))
		}
	}()

	blockMetrics, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	if err != nil {
		return fmt.Errorf("error creating metrics: %w", err)
	}

	featureFlags, err := featureflags.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create feature flags client: %w", err)
	}

	c, err := cfg.Parse()
	if err != nil {
		return fmt.Errorf("error parsing config: %w", err)
	}

	templateCache, err := sbxtemplate.NewCache(c, featureFlags, persistenceTemplate, blockMetrics)
	if err != nil {
		zap.L().Fatal("failed to create template cache", zap.Error(err))
	}
	templateCache.Start(ctx)
	defer templateCache.Stop()

	buildMetrics, err := metrics.NewBuildMetrics(noop.MeterProvider{})
	if err != nil {
		zap.L().Fatal("failed to create build metrics", zap.Error(err))
	}

	sandboxFactory := sandbox.NewFactory(c.BuilderConfig, networkPool, devicePool, featureFlags)

	builder := build.NewBuilder(
		builderConfig,
		logger,
		featureFlags,
		sandboxFactory,
		persistenceTemplate,
		persistenceBuild,
		artifactRegistry,
		dockerhubRepository,
		sandboxProxy,
		sandboxes,
		templateCache,
		buildMetrics,
	)

	logger = logger.
		With(zap.Field{Type: zapcore.StringType, Key: "envID", String: templateID}).
		With(zap.Field{Type: zapcore.StringType, Key: "buildID", String: buildID})

	force := true
	template := config.TemplateConfig{
		Version:    templates.TemplateV2LatestVersion,
		TeamID:     "",
		TemplateID: templateID,
		FromImage:  baseImage,
		Force:      &force,
		VCpuCount:  2,
		MemoryMB:   1024,
		StartCmd:   "echo 'start cmd debug' && sleep 10 && echo 'done starting command debug'",
		DiskSizeMB: 1024,
		HugePages:  true,
	}

	metadata := storage.TemplateFiles{
		BuildID:            buildID,
		KernelVersion:      kernelVersion,
		FirecrackerVersion: fcVersion,
	}
	_, err = builder.Build(ctx, metadata, template, logger.Core())
	if err != nil {
		return fmt.Errorf("error building template: %w", err)
	}

	fmt.Println("Build finished, closing...")

	return nil
}
