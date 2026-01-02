package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	"github.com/e2b-dev/infra/packages/orchestrator/internal/tcpfirewall"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	baseImage = "e2bdev/base:latest"

	defaultKernel      = "vmlinux-6.1.102"
	defaultFirecracker = "v1.12.1_717921c"

	proxyPort = 5007
)

func main() {
	ctx := context.Background()

	templateID := flag.String("template", "local-template", "template id")
	buildID := flag.String("build", "", "build id (UUID required)")
	kernelVersion := flag.String("kernel", defaultKernel, "kernel version")
	fcVersion := flag.String("firecracker", defaultFirecracker, "firecracker version")
	startCmd := flag.String("start-cmd", "", "start command to run in sandbox")
	local := flag.Bool("local", false, "use local storage (no remote resources needed)")
	dataDir := flag.String("data-dir", "", "data directory for local mode (default: .local-build)")
	flag.Parse()

	if *local {
		if *dataDir == "" {
			// Default to .local-build directory within orchestrator package
			*dataDir = ".local-build"
		}
		if err := setupLocalEnvironment(ctx, *dataDir, *kernelVersion, *fcVersion); err != nil {
			log.Fatalf("error setting up local environment: %v", err)
		}
	}

	builderConfig, err := cfg.ParseBuilder()
	if err != nil {
		log.Fatalf("error parsing builder config: %v", err)
	}

	networkConfig, err := network.ParseConfig()
	if err != nil {
		log.Fatalf("error parsing network config: %v", err)
	}

	err = buildTemplate(ctx, *kernelVersion, *fcVersion, *templateID, *buildID, *startCmd, *local, builderConfig, networkConfig)
	if err != nil {
		log.Fatalf("error building template: %v", err)
	}
}

func buildTemplate(
	parentCtx context.Context,
	kernelVersion,
	fcVersion,
	templateID,
	buildID,
	startCmd string,
	localMode bool,
	builderConfig cfg.BuilderConfig,
	networkConfig network.Config,
) error {
	ctx, cancel := context.WithTimeout(parentCtx, time.Minute*5)
	defer cancel()

	clientID := "build-template-cmd"
	var cores []zapcore.Core
	if localMode {
		// Error-only logging for local mode
		errorCore := zapcore.NewCore(
			zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
			zapcore.AddSync(os.Stderr),
			zap.ErrorLevel,
		)
		cores = append(cores, errorCore)
	}
	log, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   clientID,
		IsInternal:    true,
		IsDebug:       !localMode,
		EnableConsole: !localMode,
		Cores:         cores,
	})
	if err != nil {
		return fmt.Errorf("could not create logger: %w", err)
	}
	logger.ReplaceGlobals(ctx, log)
	sbxlogger.SetSandboxLoggerExternal(log)
	sbxlogger.SetSandboxLoggerInternal(log)

	log.Info(ctx, "building template", logger.WithTemplateID(templateID), logger.WithBuildID(buildID))

	// The sandbox map is shared between the server and the proxy
	// to propagate information about sandbox routing.
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
			log.Error(ctx, "error closing dockerhub repository", zap.Error(err))
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
		return fmt.Errorf("failed to create template cache: %w", err)
	}
	templateCache.Start(ctx)
	defer templateCache.Stop()

	buildMetrics, err := metrics.NewBuildMetrics(noop.MeterProvider{})
	if err != nil {
		return fmt.Errorf("failed to create build metrics: %w", err)
	}

	sandboxFactory := sandbox.NewFactory(c.BuilderConfig, networkPool, devicePool, featureFlags)

	builder := build.NewBuilder(
		builderConfig,
		log,
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

	log = log.
		With(zap.Field{Type: zapcore.StringType, Key: "envID", String: templateID}).
		With(zap.Field{Type: zapcore.StringType, Key: "buildID", String: buildID})

	force := true
	templateStartCmd := startCmd
	if templateStartCmd == "" {
		templateStartCmd = "echo 'sandbox ready'"
	}
	template := config.TemplateConfig{
		Version:            templates.TemplateV2LatestVersion,
		TeamID:             "",
		TemplateID:         templateID,
		FromImage:          baseImage,
		Force:              &force,
		VCpuCount:          2,
		MemoryMB:           1024,
		StartCmd:           templateStartCmd,
		DiskSizeMB:         1024,
		HugePages:          true,
		KernelVersion:      kernelVersion,
		FirecrackerVersion: fcVersion,
	}

	metadata := storage.TemplateFiles{
		BuildID: buildID,
	}
	_, err = builder.Build(ctx, metadata, template, log.Detach(ctx).Core())
	if err != nil {
		return fmt.Errorf("error building template: %w", err)
	}

	fmt.Printf("\n✅ Build finished!\n   Build ID: %s\n", buildID)

	return nil
}

// setupLocalEnvironment configures environment variables and directories for local-only operation.
func setupLocalEnvironment(ctx context.Context, dataDir, kernelVersion, fcVersion string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("local mode requires root (use sudo)")
	}

	// Setup directories - all within dataDir
	kernelsDir := filepath.Join(dataDir, "kernels")
	templatesDir := filepath.Join(dataDir, "templates")
	sandboxDir := filepath.Join(dataDir, "sandbox")
	orchestratorDir := filepath.Join(dataDir, "orchestrator")
	snapshotCacheDir := filepath.Join(dataDir, "snapshot-cache")
	fcVersionsDir := filepath.Join(dataDir, "fc-versions")
	envdDir := filepath.Join(dataDir, "envd")

	for _, dir := range []string{kernelsDir, templatesDir, sandboxDir, orchestratorDir, snapshotCacheDir, fcVersionsDir, envdDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Setup orchestrator subdirectories
	for _, subdir := range []string{"build", "build-templates", "sandbox", "snapshot-cache", "template"} {
		fullDir := filepath.Join(orchestratorDir, subdir)
		if err := os.MkdirAll(fullDir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", fullDir, err)
		}
	}

	abs := func(s string) string {
		return utils.Must(filepath.Abs(s))
	}

	// Set environment variables for local-only operation
	envVars := map[string]string{
		"ARTIFACTS_REGISTRY_PROVIDER":      "Local",
		"FIRECRACKER_VERSIONS_DIR":         abs(fcVersionsDir),
		"HOST_ENVD_PATH":                   abs(filepath.Join(envdDir, "envd")),
		"HOST_KERNELS_DIR":                 abs(kernelsDir),
		"LOCAL_TEMPLATE_STORAGE_BASE_PATH": abs(templatesDir),
		"ORCHESTRATOR_BASE_PATH":           abs(orchestratorDir),
		"SANDBOX_DIR":                      abs(sandboxDir),
		"SNAPSHOT_CACHE_DIR":               abs(snapshotCacheDir),
		"STORAGE_PROVIDER":                 "Local",
		"USE_LOCAL_NAMESPACE_STORAGE":      "true",
	}

	for k, v := range envVars {
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("failed to set env var %s: %w", k, err)
		}
	}

	// Download kernel if needed
	if err := downloadKernel(ctx, kernelsDir, kernelVersion); err != nil {
		return fmt.Errorf("failed to download kernel: %w", err)
	}

	// Download Firecracker from e2b-dev/fc-versions GitHub releases
	if err := downloadFirecracker(ctx, fcVersionsDir, fcVersion); err != nil {
		return fmt.Errorf("failed to download firecracker: %w", err)
	}

	// Copy envd from repo if needed
	if err := copyEnvd(envdDir); err != nil {
		return fmt.Errorf("failed to copy envd: %w", err)
	}

	fmt.Printf("✓ Local environment configured (data dir: %s)\n", dataDir)
	return nil
}

func downloadKernel(ctx context.Context, kernelsDir, kernelVersion string) error {
	kernelPath := filepath.Join(kernelsDir, kernelVersion, "vmlinux.bin")

	// Check if kernel already exists
	if _, err := os.Stat(kernelPath); err == nil {
		fmt.Printf("✓ Kernel %s already exists\n", kernelVersion)
		return nil
	}

	// Create kernel directory
	kernelDir := filepath.Dir(kernelPath)
	if err := os.MkdirAll(kernelDir, 0o755); err != nil {
		return fmt.Errorf("failed to create kernel directory: %w", err)
	}

	// Download kernel
	kernelURL, err := url.JoinPath("https://storage.googleapis.com/e2b-prod-public-builds/kernels/", kernelVersion, "vmlinux.bin")
	if err != nil {
		return fmt.Errorf("failed to build kernel URL: %w", err)
	}

	fmt.Printf("⬇ Downloading kernel %s...\n", kernelVersion)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kernelURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download kernel: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download kernel: HTTP %d", resp.StatusCode)
	}

	file, err := os.OpenFile(kernelPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create kernel file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("failed to write kernel file: %w", err)
	}

	fmt.Printf("✓ Kernel downloaded to %s\n", kernelPath)
	return nil
}

func downloadFirecracker(ctx context.Context, fcVersionsDir, fcVersion string) error {
	fcDir := filepath.Join(fcVersionsDir, fcVersion)
	fcPath := filepath.Join(fcDir, "firecracker")

	// Check if firecracker already exists
	if _, err := os.Stat(fcPath); err == nil {
		fmt.Printf("✓ Firecracker %s already exists\n", fcVersion)
		return nil
	}

	// Create directory
	if err := os.MkdirAll(fcDir, 0o755); err != nil {
		return fmt.Errorf("failed to create firecracker directory: %w", err)
	}

	// Download from e2b-dev/fc-versions GitHub releases
	fcURL := fmt.Sprintf("https://github.com/e2b-dev/fc-versions/releases/download/%s/firecracker", fcVersion)
	fmt.Printf("⬇ Downloading Firecracker %s from %s...\n", fcVersion, fcURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fcURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download firecracker: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download firecracker %s: HTTP %d (URL: %s)", fcVersion, resp.StatusCode, fcURL)
	}

	file, err := os.OpenFile(fcPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("failed to create firecracker file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("failed to write firecracker file: %w", err)
	}

	fmt.Printf("✓ Firecracker downloaded to %s\n", fcPath)
	return nil
}

func copyEnvd(envdDir string) error {
	envdPath := filepath.Join(envdDir, "envd")

	// Check if envd exists in the repo (relative to orchestrator package)
	repoEnvdPath, err := filepath.Abs(filepath.Join("..", "envd", "bin", "envd"))
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	srcInfo, err := os.Stat(repoEnvdPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("envd not found at %s\nBuild it first: cd ../envd && make build", repoEnvdPath)
	}
	if err != nil {
		return fmt.Errorf("failed to stat source envd: %w", err)
	}

	// Check if cached version exists and is up-to-date
	if dstInfo, err := os.Stat(envdPath); err == nil {
		if dstInfo.ModTime().After(srcInfo.ModTime()) || dstInfo.ModTime().Equal(srcInfo.ModTime()) {
			fmt.Printf("✓ Envd already exists and is up-to-date\n")
			return nil
		}
		fmt.Printf("⟳ Envd cache is stale, updating...\n")
	}

	// Copy envd from repo
	fmt.Printf("📋 Copying envd from %s...\n", repoEnvdPath)

	src, err := os.Open(repoEnvdPath)
	if err != nil {
		return fmt.Errorf("failed to open source envd: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(envdPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("failed to create envd file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy envd: %w", err)
	}

	fmt.Printf("✓ Envd copied to %s\n", envdPath)
	return nil
}
