package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const proxyPort = 5007

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	templateID := flag.String("template", "", "template id")
	buildID := flag.String("build", "", "build id")
	kernelVersion := flag.String("kernel", "", "kernel version")
	fcVersion := flag.String("firecracker", "", "firecracker version")
	flag.Parse()

	err := buildTemplate(ctx, *kernelVersion, *fcVersion, *templateID, *buildID)
	if err != nil {
		log.Fatal().Err(err).Msg("error building template")
		os.Exit(1)
	}
}

func buildTemplate(
	parentCtx context.Context,
	kernelVersion,
	fcVersion,
	templateID,
	buildID string,
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

	tracer := otel.Tracer("test")

	logger.Info("building template", l.WithTemplateID(templateID), l.WithBuildID(buildID))

	// The sandbox map is shared between the server and the proxy
	// to propagate information about sandbox routing.
	sandboxes := smap.New[*sandbox.Sandbox]()

	sandboxProxy, err := proxy.NewSandboxProxy(noop.MeterProvider{}, proxyPort, sandboxes)
	if err != nil {
		logger.Fatal("failed to create sandbox proxy", zap.Error(err))
	}
	go func() {
		err := sandboxProxy.Start()
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

	persistenceTemplate, err := storage.GetTemplateStorageProvider(ctx, nil, block.ChunkSize)
	if err != nil {
		return fmt.Errorf("could not create storage provider: %w", err)
	}

	persistenceBuild, err := storage.GetBuildCacheStorageProvider(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not create storage provider: %w", err)
	}

	devicePool, err := nbd.NewDevicePool(ctx, noop.MeterProvider{})
	if err != nil {
		return fmt.Errorf("could not create device pool: %w", err)
	}
	defer func() {
		err := devicePool.Close(parentCtx)
		if err != nil {
			logger.Error("error closing device pool", zap.Error(err))
		}
	}()

	networkPool, err := network.NewPool(ctx, noop.MeterProvider{}, 8, 8, clientID, tracer)
	if err != nil {
		return fmt.Errorf("could not create network pool: %w", err)
	}
	defer func() {
		err := networkPool.Close(parentCtx)
		if err != nil {
			logger.Error("error closing network pool", zap.Error(err))
		}
	}()

	artifactRegistry, err := artifactsregistry.GetArtifactsRegistryProvider()
	if err != nil {
		return fmt.Errorf("error getting artifacts registry provider: %v", err)
	}

	templateCache, err := sbxtemplate.NewCache(ctx, persistenceTemplate)
	if err != nil {
		zap.L().Fatal("failed to create template cache", zap.Error(err))
	}

	builder := build.NewBuilder(
		logger,
		tracer,
		persistenceTemplate,
		persistenceBuild,
		artifactRegistry,
		devicePool,
		networkPool,
		sandboxProxy,
		sandboxes,
		templateCache,
	)

	logsWriter := logger.
		With(zap.Field{Type: zapcore.StringType, Key: "envID", String: templateID}).
		With(zap.Field{Type: zapcore.StringType, Key: "buildID", String: buildID})

	template := config.TemplateConfig{
		VCpuCount:  2,
		MemoryMB:   1024,
		StartCmd:   "echo 'start cmd debug' && sleep 10 && echo 'done starting command debug'",
		DiskSizeMB: 1024,
		HugePages:  true,
	}

	metadata := storage.TemplateFiles{
		TemplateID:         templateID,
		BuildID:            buildID,
		KernelVersion:      kernelVersion,
		FirecrackerVersion: fcVersion,
	}
	_, err = builder.Build(ctx, metadata, template, logsWriter)
	if err != nil {
		return fmt.Errorf("error building template: %w", err)
	}

	fmt.Println("Build finished, closing...")
	return nil
}
