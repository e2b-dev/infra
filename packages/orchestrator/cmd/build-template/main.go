package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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

func buildTemplate(parentCtx context.Context, kernelVersion, fcVersion, templateID, buildID string) error {
	ctx, cancel := context.WithTimeout(parentCtx, time.Minute*5)
	defer cancel()

	clientID := "build-template-cmd"
	logger, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName: clientID,
		IsInternal:  true,
		IsDebug:     true,
	})
	if err != nil {
		return fmt.Errorf("could not create logger: %w", err)
	}
	zap.ReplaceGlobals(logger)
	sbxlogger.SetSandboxLoggerExternal(logger)
	sbxlogger.SetSandboxLoggerInternal(logger)

	tracer := otel.Tracer("test")

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("could not create docker client: %w", err)
	}

	legacyClient, err := docker.NewClientFromEnv()
	if err != nil {
		return fmt.Errorf("could not create docker legacy client: %w", err)
	}

	// The sandbox map is shared between the server and the proxy
	// to propagate information about sandbox routing.
	sandboxes := smap.New[*sandbox.Sandbox]()

	sandboxProxy, err := proxy.NewSandboxProxy(proxyPort, sandboxes)
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

	persistence, err := storage.GetTemplateStorageProvider(ctx)
	if err != nil {
		return fmt.Errorf("could not create storage provider: %w", err)
	}

	devicePool, err := nbd.NewDevicePool(ctx)
	if err != nil {
		return fmt.Errorf("could not create device pool: %w", err)
	}
	defer func() {
		err := devicePool.Close(parentCtx)
		if err != nil {
			logger.Error("error closing device pool", zap.Error(err))
		}
	}()

	networkPool, err := network.NewPool(ctx, 8, 8, clientID, tracer)
	if err != nil {
		return fmt.Errorf("could not create network pool: %w", err)
	}
	defer func() {
		err := networkPool.Close(parentCtx)
		if err != nil {
			logger.Error("error closing network pool", zap.Error(err))
		}
	}()

	templateStorage := template.NewStorage(persistence)
	buildCache := cache.NewBuildCache()
	builder := build.NewBuilder(
		logger,
		logger,
		tracer,
		dockerClient,
		legacyClient,
		templateStorage,
		buildCache,
		persistence,
		devicePool,
		networkPool,
		sandboxProxy,
		sandboxes,
	)

	err = buildCache.Create(buildID, templateID)
	if err != nil {
		return fmt.Errorf("error while creating build cache: %w", err)
	}

	logsWriter := writer.New(
		logger.
			With(zap.Field{Type: zapcore.StringType, Key: "envID", String: templateID}).
			With(zap.Field{Type: zapcore.StringType, Key: "buildID", String: buildID}),
	)
	config := &build.TemplateConfig{
		TemplateFiles: storage.NewTemplateFiles(
			templateID,
			buildID,
			kernelVersion,
			fcVersion,
		),
		VCpuCount:       2,
		MemoryMB:        1024,
		StartCmd:        "echo 'start cmd debug' && sleep 10 && echo 'done starting command debug'",
		DiskSizeMB:      1024,
		BuildLogsWriter: logsWriter,
		HugePages:       true,
	}

	err = builder.Build(ctx, config, templateID, buildID)
	if err != nil {
		return fmt.Errorf("error building template: %w", err)
	}

	fmt.Println("Build finished, closing...")
	return nil
}
