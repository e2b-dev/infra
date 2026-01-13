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
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/tcpfirewall"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/optimize"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	proxyPort        = 5007
	operationTimeout = 10 * time.Minute
)

func main() {
	ctx := context.Background()

	buildID := flag.String("build", "", "build id of the template to optimize (required)")
	vcpu := flag.Int64("vcpu", 0, "number of vCPUs (required)")
	memoryMB := flag.Int64("memory", 0, "memory in MB (required)")
	kernelVersion := flag.String("kernel", "", "kernel version (optional, read from metadata if not provided)")
	fcVersion := flag.String("firecracker", "", "firecracker version (optional, read from metadata if not provided)")
	flag.Parse()

	if *buildID == "" {
		log.Fatal("build id is required (-build)")
	}
	if *vcpu <= 0 {
		log.Fatal("vcpu must be positive (-vcpu)")
	}
	if *memoryMB <= 0 {
		log.Fatal("memory must be positive (-memory)")
	}

	builderConfig, err := cfg.ParseBuilder()
	if err != nil {
		log.Fatalf("error parsing builder config: %v", err)
	}

	networkConfig, err := network.ParseConfig()
	if err != nil {
		log.Fatalf("error parsing network config: %v", err)
	}

	err = optimizeBuild(ctx, *buildID, *vcpu, *memoryMB, *kernelVersion, *fcVersion, builderConfig, networkConfig)
	if err != nil {
		log.Fatalf("error optimizing build: %v", err)
	}
}

func optimizeBuild(
	parentCtx context.Context,
	buildID string,
	vcpu int64,
	memoryMB int64,
	kernelVersion string,
	fcVersion string,
	builderConfig cfg.BuilderConfig,
	networkConfig network.Config,
) error {
	ctx, cancel := context.WithTimeout(parentCtx, operationTimeout)
	defer cancel()

	clientID := "optimize-build-cmd"
	log, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   clientID,
		IsInternal:    true,
		IsDebug:       true,
		EnableConsole: true,
	})
	if err != nil {
		return fmt.Errorf("could not create logger: %w", err)
	}
	logger.ReplaceGlobals(ctx, log)
	sbxlogger.SetSandboxLoggerExternal(log)
	sbxlogger.SetSandboxLoggerInternal(log)

	log.Info(ctx, "optimizing build", logger.WithBuildID(buildID))

	// Create storage provider
	templateStorage, err := storage.GetTemplateStorageProvider(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not create storage provider: %w", err)
	}

	// Load existing metadata from storage
	existingMetadata, err := metadata.FromBuildID(ctx, templateStorage, buildID)
	if err != nil {
		return fmt.Errorf("could not load metadata for build %s: %w", buildID, err)
	}

	log.Info(ctx, "loaded existing metadata",
		zap.String("build_id", existingMetadata.Template.BuildID),
		zap.String("kernel_version", existingMetadata.Template.KernelVersion),
		zap.String("firecracker_version", existingMetadata.Template.FirecrackerVersion),
	)

	// Use kernel/firecracker versions from metadata if not provided
	if kernelVersion == "" {
		kernelVersion = existingMetadata.Template.KernelVersion
	}
	if fcVersion == "" {
		fcVersion = existingMetadata.Template.FirecrackerVersion
	}

	if kernelVersion == "" || fcVersion == "" {
		return fmt.Errorf("kernel and firecracker versions are required (either from flags or metadata)")
	}

	// The sandbox map is shared between the server and the proxy
	sandboxes := sandbox.NewSandboxesMap()

	sandboxProxy, err := proxy.NewSandboxProxy(noop.MeterProvider{}, proxyPort, sandboxes)
	if err != nil {
		return fmt.Errorf("failed to create sandbox proxy: %w", err)
	}
	go func() {
		err := sandboxProxy.Start(parentCtx)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(ctx, "failed to start sandbox proxy", zap.Error(err))
		}
	}()
	defer func() {
		err := sandboxProxy.Close(parentCtx)
		if err != nil {
			log.Error(ctx, "error closing sandbox proxy", zap.Error(err))
		}
	}()

	// hostname egress filter proxy
	tcpFirewall := tcpfirewall.New(
		log,
		networkConfig,
		sandboxes,
		noop.NewMeterProvider(),
	)
	go func() {
		err := tcpFirewall.Start(ctx)
		if err != nil {
			log.Error(ctx, "error starting tcp egress firewall", zap.Error(err))
		}
	}()
	defer func() {
		err := tcpFirewall.Close(parentCtx)
		if err != nil {
			log.Error(ctx, "error closing tcp egress firewall", zap.Error(err))
		}
	}()

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return fmt.Errorf("could not create device pool: %w", err)
	}
	go func() {
		devicePool.Populate(ctx)
		log.Info(ctx, "device pool done populating")
	}()
	defer func() {
		if err := devicePool.Close(parentCtx); err != nil {
			log.Error(ctx, "error closing device pool", zap.Error(err))
		}
	}()

	slotStorage, err := network.NewStorageLocal(ctx, networkConfig)
	if err != nil {
		return fmt.Errorf("could not create network pool: %w", err)
	}
	networkPool := network.NewPool(8, 8, slotStorage, networkConfig)
	go func() {
		networkPool.Populate(ctx)
		log.Info(ctx, "network pool done populating")
	}()
	defer func() {
		err := networkPool.Close(parentCtx)
		if err != nil {
			log.Error(ctx, "error closing network pool", zap.Error(err))
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

	templateCache, err := sbxtemplate.NewCache(c, featureFlags, templateStorage, blockMetrics)
	if err != nil {
		return fmt.Errorf("failed to create template cache: %w", err)
	}
	templateCache.Start(ctx)
	defer templateCache.Stop()

	envdVersion, err := envd.GetEnvdVersion(ctx, builderConfig.HostEnvdPath)
	if err != nil {
		return fmt.Errorf("error getting envd version: %w", err)
	}

	sandboxFactory := sandbox.NewFactory(c.BuilderConfig, networkPool, devicePool, featureFlags)

	log.Info(ctx, "starting optimize phase",
		zap.Int64("vcpu", vcpu),
		zap.Int64("memory_mb", memoryMB),
		zap.String("kernel_version", kernelVersion),
		zap.String("firecracker_version", fcVersion),
		zap.String("envd_version", envdVersion),
	)

	// Create build context
	uploadErrGroup := &errgroup.Group{}
	templateConfig := config.TemplateConfig{
		TemplateID:         "",
		VCpuCount:          vcpu,
		MemoryMB:           memoryMB,
		HugePages:          true, // Always enabled
		KernelVersion:      kernelVersion,
		FirecrackerVersion: fcVersion,
	}

	bc := buildcontext.BuildContext{
		BuilderConfig:  builderConfig,
		Config:         templateConfig,
		Template:       storage.TemplateFiles{BuildID: buildID},
		UploadErrGroup: uploadErrGroup,
		EnvdVersion:    envdVersion,
	}

	// Create layer executor
	layerExecutor := layer.NewLayerExecutor(
		bc,
		log,
		templateCache,
		sandboxProxy,
		sandboxes,
		templateStorage,
		nil, // buildStorage not needed for optimize
		nil, // index not needed for optimize
	)

	// Create the optimize builder (reusing the existing phase)
	// Network is disabled to prevent internet access during optimization
	optimizeBuilder := optimize.New(
		bc,
		sandboxFactory,
		templateStorage,
		templateCache,
		sandboxProxy,
		layerExecutor,
		sandboxes,
		log,
		optimize.WithDisableNetwork(),
	)

	// Create source layer from existing metadata
	sourceLayer := phases.LayerResult{
		Metadata: existingMetadata,
		Cached:   false,
		Hash:     buildID, // Use buildID as the base hash
	}

	// Get the hash for the optimize phase
	hash, err := optimizeBuilder.Hash(ctx, sourceLayer)
	if err != nil {
		return fmt.Errorf("failed to compute hash: %w", err)
	}

	// Get the current layer
	currentLayer, err := optimizeBuilder.Layer(ctx, sourceLayer, hash)
	if err != nil {
		return fmt.Errorf("failed to get layer: %w", err)
	}

	// Run the optimize phase
	result, err := optimizeBuilder.Build(ctx, log, "optimize", sourceLayer, currentLayer)
	if err != nil {
		return fmt.Errorf("optimize phase failed: %w", err)
	}

	blockCount := 0
	if result.Metadata.Prefetch != nil && result.Metadata.Prefetch.Memory != nil {
		blockCount = result.Metadata.Prefetch.Memory.Count()
	}

	log.Info(ctx, fmt.Sprintf("Optimize finished successfully. Collected prefetch mapping with %d memory blocks", blockCount))

	return nil
}
