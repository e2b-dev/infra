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
	"strings"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldlog"
	"go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
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
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	baseImage     = "e2bdev/base:latest"
	defaultKernel = "vmlinux-6.1.102"
	defaultFC     = "v1.12.1_717921c"
	proxyPort     = 5007
)

func main() {
	templateID := flag.String("template", "local-template", "template id")
	fromBuild := flag.String("from-build", "", "base build ID to build from (incremental build)")
	toBuild := flag.String("to-build", "", "output build ID (UUID, required)")
	storagePath := flag.String("storage", "", "storage: local path or gs://bucket (default: gs://$TEMPLATE_BUCKET_NAME or .local-build)")
	kernel := flag.String("kernel", defaultKernel, "kernel version")
	fc := flag.String("firecracker", defaultFC, "firecracker version")
	vcpu := flag.Int("vcpu", 2, "vCPUs")
	memory := flag.Int("memory", 1024, "memory MB")
	disk := flag.Int("disk", 1024, "disk MB")
	hugePages := flag.Bool("hugepages", true, "use 2MB huge pages for memory (false = 4KB pages)")
	startCmd := flag.String("start-cmd", "", "start command")
	setupCmd := flag.String("setup-cmd", "", "setup command to run during build (e.g., install deps)")
	readyCmd := flag.String("ready-cmd", "", "ready check command")
	verbose := flag.Bool("v", false, "verbose output")
	flag.Parse()

	if *toBuild == "" {
		log.Fatal("-to-build required")
	}

	// Always suppress OTEL tracing logs
	cmdutil.SuppressOTELLogs()

	// Suppress other noisy output unless verbose, but keep std log for fatal errors
	if !*verbose {
		cmdutil.SuppressNoisyLogsKeepStdLog()
	}

	ctx := context.Background()

	// Only run setupEnv when -storage is explicitly passed (for local development)
	// Otherwise, use existing environment variables (like the original code did)
	localMode := false
	if *storagePath != "" {
		localMode = !strings.HasPrefix(*storagePath, "gs://")
		if err := setupEnv(ctx, *storagePath, *kernel, *fc, localMode); err != nil {
			log.Fatal(err)
		}
	}

	builderConfig, err := cfg.ParseBuilder()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	networkConfig, err := network.ParseConfig()
	if err != nil {
		log.Fatalf("network config: %v", err)
	}

	err = doBuild(ctx, *templateID, *toBuild, *fromBuild, *kernel, *fc, *vcpu, *memory, *disk, *hugePages, *startCmd, *setupCmd, *readyCmd, localMode, *verbose, builderConfig, networkConfig)
	if err != nil {
		log.Fatal(err)
	}
}

func setupEnv(ctx context.Context, storagePath, kernel, fc string, localMode bool) error {
	abs := func(s string) string { return utils.Must(filepath.Abs(s)) }

	if localMode {
		if os.Geteuid() != 0 {
			return fmt.Errorf("local mode requires root")
		}

		dataDir := storagePath
		dirs := []string{"kernels", "templates", "sandbox", "orchestrator", "snapshot-cache", "fc-versions"}
		for _, d := range dirs {
			if err := os.MkdirAll(filepath.Join(dataDir, d), 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", d, err)
			}
		}
		for _, d := range []string{"build", "build-templates", "sandbox", "snapshot-cache", "template"} {
			if err := os.MkdirAll(filepath.Join(dataDir, "orchestrator", d), 0o755); err != nil {
				return fmt.Errorf("mkdir orchestrator/%s: %w", d, err)
			}
		}

		env := map[string]string{
			"ARTIFACTS_REGISTRY_PROVIDER":      "Local",
			"FIRECRACKER_VERSIONS_DIR":         abs(filepath.Join(dataDir, "fc-versions")),
			"HOST_KERNELS_DIR":                 abs(filepath.Join(dataDir, "kernels")),
			"LOCAL_TEMPLATE_STORAGE_BASE_PATH": abs(filepath.Join(dataDir, "templates")),
			"ORCHESTRATOR_BASE_PATH":           abs(filepath.Join(dataDir, "orchestrator")),
			"SANDBOX_DIR":                      abs(filepath.Join(dataDir, "sandbox")),
			"SNAPSHOT_CACHE_DIR":               abs(filepath.Join(dataDir, "snapshot-cache")),
			"STORAGE_PROVIDER":                 "Local",
			"USE_LOCAL_NAMESPACE_STORAGE":      "true",
		}
		for k, v := range env {
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}

		if err := setupKernel(ctx, filepath.Join(dataDir, "kernels"), kernel); err != nil {
			return err
		}
		if err := setupFC(ctx, filepath.Join(dataDir, "fc-versions"), fc); err != nil {
			return err
		}

		// HOST_ENVD_PATH: use env if set, otherwise default to local dev path
		envdPath := os.Getenv("HOST_ENVD_PATH")
		if envdPath == "" {
			envdPath = abs("../envd/bin/envd")
			os.Setenv("HOST_ENVD_PATH", envdPath)
		}
		if _, err := os.Stat(envdPath); err == nil {
			fmt.Printf("✓ Envd: %s\n", envdPath)
		}

		fmt.Printf("✓ Storage: %s (local)\n", dataDir)
	} else {
		bucket := strings.TrimPrefix(storagePath, "gs://")
		if os.Getenv("STORAGE_PROVIDER") == "" {
			os.Setenv("STORAGE_PROVIDER", "GCPBucket")
		}
		if os.Getenv("TEMPLATE_BUCKET_NAME") == "" {
			os.Setenv("TEMPLATE_BUCKET_NAME", bucket)
		}
		fmt.Printf("✓ Storage: gs://%s\n", bucket)
	}

	return nil
}

func doBuild(
	parentCtx context.Context,
	templateID, buildID, fromBuild, kernel, fc string,
	vcpu, memory, disk int,
	hugePages bool,
	startCmd, setupCmd, readyCmd string,
	localMode, verbose bool,
	builderConfig cfg.BuilderConfig,
	networkConfig network.Config,
) error {
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Minute)
	defer cancel()

	var cores []zapcore.Core
	if localMode && !verbose {
		// Only log errors when in local mode without verbose
		cores = append(cores, zapcore.NewCore(
			zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
			zapcore.AddSync(os.Stderr),
			zap.ErrorLevel,
		))
	}

	l, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   "build-template",
		IsInternal:    true,
		IsDebug:       verbose,
		EnableConsole: verbose,
		Cores:         cores,
	})
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}
	logger.ReplaceGlobals(ctx, l)
	sbxlogger.SetSandboxLoggerExternal(l)
	if verbose {
		sbxlogger.SetSandboxLoggerInternal(l)
	} else {
		sbxlogger.SetSandboxLoggerInternal(logger.NewNopLogger())
	}

	l.Info(ctx, "building template", logger.WithTemplateID(templateID), logger.WithBuildID(buildID))

	logLevel := ldlog.Error
	if verbose {
		logLevel = ldlog.Info
	}
	featureFlags, _ := featureflags.NewClientWithLogLevel(logLevel)

	sandboxes := sandbox.NewSandboxesMap()

	sandboxProxy, err := proxy.NewSandboxProxy(noop.MeterProvider{}, proxyPort, sandboxes)
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}
	go func() {
		if err := sandboxProxy.Start(parentCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.Error(ctx, "proxy error", zap.Error(err))
		}
	}()
	defer sandboxProxy.Close(parentCtx)

	tcpFirewall := tcpfirewall.New(l, networkConfig, sandboxes, noop.NewMeterProvider(), featureFlags)
	go tcpFirewall.Start(ctx)
	defer tcpFirewall.Close(parentCtx)

	persistenceTemplate, err := storage.GetTemplateStorageProvider(ctx, nil)
	if err != nil {
		return fmt.Errorf("template storage: %w", err)
	}
	persistenceBuild, err := storage.GetBuildCacheStorageProvider(ctx, nil)
	if err != nil {
		return fmt.Errorf("build storage: %w", err)
	}

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return fmt.Errorf("nbd pool: %w", err)
	}
	go devicePool.Populate(ctx)
	defer devicePool.Close(parentCtx)

	slotStorage, err := network.NewStorageLocal(ctx, networkConfig)
	if err != nil {
		return fmt.Errorf("network storage: %w", err)
	}
	networkPool := network.NewPool(8, 8, slotStorage, networkConfig)
	go networkPool.Populate(ctx)
	defer networkPool.Close(parentCtx)

	artifactRegistry, err := artifactsregistry.GetArtifactsRegistryProvider(ctx)
	if err != nil {
		return fmt.Errorf("artifacts registry: %w", err)
	}

	dockerhubRepo, err := dockerhub.GetRemoteRepository(ctx)
	if err != nil {
		return fmt.Errorf("dockerhub: %w", err)
	}
	defer dockerhubRepo.Close()

	blockMetrics, _ := blockmetrics.NewMetrics(noop.NewMeterProvider())

	c, err := cfg.Parse()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	templateCache, err := sbxtemplate.NewCache(c, featureFlags, persistenceTemplate, blockMetrics)
	if err != nil {
		return fmt.Errorf("template cache: %w", err)
	}
	templateCache.Start(ctx)
	defer templateCache.Stop()

	buildMetrics, _ := metrics.NewBuildMetrics(noop.MeterProvider{})
	sandboxFactory := sandbox.NewFactory(c.BuilderConfig, networkPool, devicePool, featureFlags)

	builder := build.NewBuilder(
		builderConfig, l, featureFlags, sandboxFactory,
		persistenceTemplate, persistenceBuild, artifactRegistry,
		dockerhubRepo, sandboxProxy, sandboxes, templateCache, buildMetrics,
	)

	l = l.With(zap.String("envID", templateID)).With(zap.String("buildID", buildID))

	force := true

	// Build steps list for setup commands
	var steps []*templatemanager.TemplateStep
	if setupCmd != "" {
		fmt.Printf("Setup command (as root): %s\n", setupCmd)
		// Add a RUN step that executes as root
		steps = append(steps, &templatemanager.TemplateStep{
			Type: "RUN",
			Args: []string{setupCmd, "root"}, // command, user
		})
	}

	tmpl := config.TemplateConfig{
		Version:            templates.TemplateV2LatestVersion,
		TemplateID:         templateID,
		Force:              &force,
		VCpuCount:          int64(vcpu),
		MemoryMB:           int64(memory),
		DiskSizeMB:         int64(disk),
		HugePages:          hugePages,
		StartCmd:           startCmd,
		ReadyCmd:           readyCmd,
		KernelVersion:      kernel,
		FirecrackerVersion: fc,
		Steps:              steps,
	}

	pageSizeStr := "2MB (hugepages)"
	if !hugePages {
		pageSizeStr = "4KB (standard)"
	}
	fmt.Printf("Building with page size: %s\n", pageSizeStr)

	if fromBuild != "" {
		tmpl.FromTemplate = &templatemanager.FromTemplateConfig{BuildID: fromBuild}
		fmt.Printf("Building from: %s\n", fromBuild)
	} else {
		tmpl.FromImage = baseImage
	}

	result, err := builder.Build(ctx, storage.TemplateFiles{BuildID: buildID}, tmpl, l.Detach(ctx).Core())
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}

	fmt.Printf("\n✅ Build finished: %s\n", buildID)

	// Print artifact sizes
	printArtifactSizes(ctx, persistenceTemplate, buildID, result)

	return nil
}

func printArtifactSizes(ctx context.Context, persistence storage.StorageProvider, buildID string, _ *build.Result) {
	files := storage.TemplateFiles{BuildID: buildID}
	basePath := os.Getenv("LOCAL_TEMPLATE_STORAGE_BASE_PATH")

	fmt.Printf("\n📦 Artifacts:\n")

	// For local storage, get actual file sizes on disk
	if basePath != "" {
		printLocalFileSizes(basePath, buildID)
	} else {
		// For remote storage, get sizes from storage provider
		if size, _, err := persistence.Size(ctx, files.StorageMemfilePath()); err == nil {
			fmt.Printf("   Memfile: %d MB\n", size>>20)
		}
	}
}

func printLocalFileSizes(basePath, buildID string) {
	dir := filepath.Join(basePath, buildID)

	for _, a := range cmdutil.MainArtifacts() {
		path := filepath.Join(dir, a.File)
		_, actual, err := cmdutil.GetFileSizes(path)
		if err != nil {
			continue
		}

		headerPath := filepath.Join(dir, a.HeaderFile)
		totalSize, blockSize := cmdutil.GetHeaderInfo(headerPath)
		if totalSize == 0 {
			fmt.Printf("   %s: %d MB (this layer)\n", a.Name, actual>>20)

			continue
		}

		pct := float64(actual) / float64(totalSize) * 100
		fmt.Printf("   %s: %d MB diff / %d MB total (%.1f%%), block size: %d KB\n",
			a.Name, actual>>20, totalSize>>20, pct, blockSize>>10)
	}

	for _, a := range cmdutil.SmallArtifacts() {
		path := filepath.Join(dir, a.File)
		if actual, err := cmdutil.GetActualFileSize(path); err == nil {
			fmt.Printf("   %s: %d KB\n", a.Name, actual>>10)
		}
	}
}

func setupKernel(ctx context.Context, dir, version string) error {
	dstPath := filepath.Join(dir, version, "vmlinux.bin")
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("mkdir kernel dir: %w", err)
	}

	if _, err := os.Stat(dstPath); err == nil {
		fmt.Printf("✓ Kernel %s exists\n", version)

		return nil
	}

	kernelURL, _ := url.JoinPath("https://storage.googleapis.com/e2b-prod-public-builds/kernels/", version, "vmlinux.bin")
	fmt.Printf("⬇ Downloading kernel %s...\n", version)

	return download(ctx, kernelURL, dstPath, 0o644)
}

func setupFC(ctx context.Context, dir, version string) error {
	dstPath := filepath.Join(dir, version, "firecracker")
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("mkdir firecracker dir: %w", err)
	}

	if _, err := os.Stat(dstPath); err == nil {
		fmt.Printf("✓ Firecracker %s exists\n", version)

		return nil
	}

	fcURL := fmt.Sprintf("https://github.com/e2b-dev/fc-versions/releases/download/%s/firecracker", version)
	fmt.Printf("⬇ Downloading Firecracker %s...\n", version)

	return download(ctx, fcURL, dstPath, 0o755)
}

func download(ctx context.Context, url, path string, perm os.FileMode) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	if err == nil {
		fmt.Printf("✓ Downloaded %s\n", filepath.Base(path))
	}

	return err
}
