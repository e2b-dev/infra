package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

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

func buildTemplate(ctx context.Context, kernelVersion, fcVersion, templateID, buildID string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute*3)
	defer cancel()

	clientID := "build-template-cmd"
	logger, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName: clientID,
		IsInternal:  true,
		IsDebug:     env.IsDebug(),
	})
	if err != nil {
		panic(err)
	}

	tracer := otel.Tracer("test")

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("could not create docker client: %w", err)
	}

	legacyClient, err := docker.NewClientFromEnv()
	if err != nil {
		return fmt.Errorf("could not create docker legacy client: %w", err)
	}

	persistence, err := storage.GetTemplateStorageProvider(ctx)
	if err != nil {
		return fmt.Errorf("could not create storage provider: %w", err)
	}

	networkPool, err := network.NewPool(ctx, network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, clientID)
	if err != nil {
		return fmt.Errorf("could not create network pool: %w", err)
	}
	defer networkPool.Close(ctx)

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return fmt.Errorf("could not create device pool: %w", err)
	}
	defer devicePool.Close(ctx)

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
	)

	var buf bytes.Buffer
	config := &build.TemplateConfig{
		TemplateFiles: storage.NewTemplateFiles(
			templateID,
			buildID,
			kernelVersion,
			fcVersion,
		),
		VCpuCount:       2,
		MemoryMB:        1024,
		StartCmd:        "",
		DiskSizeMB:      1024,
		BuildLogsWriter: &buf,
		HugePages:       true,
	}

	return builder.Build(ctx, config, templateID, buildID)
}
