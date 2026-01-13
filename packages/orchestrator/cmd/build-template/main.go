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
	buildID := flag.String("build", "", "build id (UUID)")
	fromBuild := flag.String("from-build", "", "base build ID to rebuild from")
	storagePath := flag.String("storage", ".local-build", "storage: local path or gs://bucket")
	kernel := flag.String("kernel", defaultKernel, "kernel version")
	kernelPath := flag.String("kernel-path", "", "local path to kernel binary (overrides -kernel)")
	fc := flag.String("firecracker", defaultFC, "firecracker version")
	fcPath := flag.String("firecracker-path", "", "local path to firecracker binary (overrides -firecracker)")
	envdPath := flag.String("envd", "", "path to envd binary (default: ../envd/bin/envd)")
	vcpu := flag.Int("vcpu", 1, "vCPUs")
	memory := flag.Int("memory", 512, "memory MB")
	disk := flag.Int("disk", 1000, "disk MB")
	hugePages := flag.Bool("hugepages", true, "huge pages")
	startCmd := flag.String("start-cmd", "", "start command")
	readyCmd := flag.String("ready-cmd", "", "ready check command")
	flag.Parse()

	ctx := context.Background()

	localMode := !strings.HasPrefix(*storagePath, "gs://")
	if err := setupEnv(ctx, *storagePath, *kernel, *kernelPath, *fc, *fcPath, *envdPath, localMode); err != nil {
		log.Fatal(err)
	}

	builderConfig, err := cfg.ParseBuilder()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	networkConfig, err := network.ParseConfig()
	if err != nil {
		log.Fatalf("network config: %v", err)
	}

	err = doBuild(ctx, *templateID, *buildID, *fromBuild, *kernel, *fc, *vcpu, *memory, *disk, *hugePages, *startCmd, *readyCmd, localMode, builderConfig, networkConfig)
	if err != nil {
		log.Fatal(err)
	}
}

func setupEnv(ctx context.Context, storagePath, kernel, kernelPath, fc, fcPath, envdPath string, localMode bool) error {
	abs := func(s string) string { return utils.Must(filepath.Abs(s)) }

	if localMode {
		if os.Geteuid() != 0 {
			return fmt.Errorf("local mode requires root")
		}

		dataDir := storagePath
		dirs := []string{"kernels", "templates", "sandbox", "orchestrator", "snapshot-cache", "fc-versions", "envd"}
		for _, d := range dirs {
			os.MkdirAll(filepath.Join(dataDir, d), 0o755)
		}
		for _, d := range []string{"build", "build-templates", "sandbox", "snapshot-cache", "template"} {
			os.MkdirAll(filepath.Join(dataDir, "orchestrator", d), 0o755)
		}

		env := map[string]string{
			"ARTIFACTS_REGISTRY_PROVIDER":      "Local",
			"FIRECRACKER_VERSIONS_DIR":         abs(filepath.Join(dataDir, "fc-versions")),
			"HOST_ENVD_PATH":                   abs(filepath.Join(dataDir, "envd", "envd")),
			"HOST_KERNELS_DIR":                 abs(filepath.Join(dataDir, "kernels")),
			"LOCAL_TEMPLATE_STORAGE_BASE_PATH": abs(filepath.Join(dataDir, "templates")),
			"ORCHESTRATOR_BASE_PATH":           abs(filepath.Join(dataDir, "orchestrator")),
			"SANDBOX_DIR":                      abs(filepath.Join(dataDir, "sandbox")),
			"SNAPSHOT_CACHE_DIR":               abs(filepath.Join(dataDir, "snapshot-cache")),
			"STORAGE_PROVIDER":                 "Local",
			"USE_LOCAL_NAMESPACE_STORAGE":      "true",
		}
		for k, v := range env {
			os.Setenv(k, v)
		}

		if err := setupKernel(ctx, filepath.Join(dataDir, "kernels"), kernel, kernelPath); err != nil {
			return err
		}
		if err := setupFC(ctx, filepath.Join(dataDir, "fc-versions"), fc, fcPath); err != nil {
			return err
		}
		if err := copyBinary(filepath.Join(dataDir, "envd", "envd"), envdPath, "../envd/bin/envd", "Envd"); err != nil {
			return err
		}

		fmt.Printf("✓ Storage: %s (local)\n", dataDir)
	} else {
		bucket := strings.TrimPrefix(storagePath, "gs://")
		os.Setenv("STORAGE_PROVIDER", "GCPBucket")
		os.Setenv("TEMPLATE_BUCKET_NAME", bucket)
		fmt.Printf("✓ Storage: gs://%s\n", bucket)
	}

	return nil
}

func doBuild(
	ctx context.Context,
	templateID, buildID, fromBuild, kernel, fc string,
	vcpu, memory, disk int,
	hugePages bool,
	startCmd, readyCmd string,
	localMode bool,
	builderConfig cfg.BuilderConfig,
	networkConfig network.Config,
) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var cores []zapcore.Core
	if localMode {
		cores = append(cores, zapcore.NewCore(
			zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
			zapcore.AddSync(os.Stderr),
			zap.ErrorLevel,
		))
	}

	l, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   "build-template",
		IsInternal:    true,
		IsDebug:       !localMode,
		EnableConsole: !localMode,
		Cores:         cores,
	})
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}
	logger.ReplaceGlobals(ctx, l)
	sbxlogger.SetSandboxLoggerExternal(l)
	sbxlogger.SetSandboxLoggerInternal(l)

	l.Info(ctx, "building template", logger.WithTemplateID(templateID), logger.WithBuildID(buildID))

	sandboxes := sandbox.NewSandboxesMap()

	sandboxProxy, err := proxy.NewSandboxProxy(noop.MeterProvider{}, proxyPort, sandboxes)
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}
	go func() {
		if err := sandboxProxy.Start(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.Error(ctx, "proxy error", zap.Error(err))
		}
	}()
	defer sandboxProxy.Close(ctx)

	tcpFirewall := tcpfirewall.New(l, networkConfig, sandboxes, noop.NewMeterProvider())
	go tcpFirewall.Start(ctx)
	defer tcpFirewall.Close(ctx)

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
	defer devicePool.Close(ctx)

	slotStorage, err := network.NewStorageLocal(ctx, networkConfig)
	if err != nil {
		return fmt.Errorf("network storage: %w", err)
	}
	networkPool := network.NewPool(8, 8, slotStorage, networkConfig)
	go networkPool.Populate(ctx)
	defer networkPool.Close(ctx)

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
	featureFlags, _ := featureflags.NewClient()

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
	if startCmd == "" {
		startCmd = "echo 'sandbox ready'"
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
	}

	if fromBuild != "" {
		tmpl.FromTemplate = &templatemanager.FromTemplateConfig{BuildID: fromBuild}
		fmt.Printf("Building from: %s\n", fromBuild)
	} else {
		tmpl.FromImage = baseImage
	}

	_, err = builder.Build(ctx, storage.TemplateFiles{BuildID: buildID}, tmpl, l.Detach(ctx).Core())
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}

	fmt.Printf("\n✅ Build finished: %s\n", buildID)

	return nil
}

func setupKernel(ctx context.Context, dir, version, localPath string) error {
	dstPath := filepath.Join(dir, version, "vmlinux.bin")
	os.MkdirAll(filepath.Dir(dstPath), 0o755)

	if localPath != "" {
		return copyFile(dstPath, localPath, "Kernel", 0o644)
	}

	if _, err := os.Stat(dstPath); err == nil {
		fmt.Printf("✓ Kernel %s exists\n", version)

		return nil
	}

	kernelURL, _ := url.JoinPath("https://storage.googleapis.com/e2b-prod-public-builds/kernels/", version, "vmlinux.bin")
	fmt.Printf("⬇ Downloading kernel %s...\n", version)

	return download(ctx, kernelURL, dstPath, 0o644)
}

func setupFC(ctx context.Context, dir, version, localPath string) error {
	dstPath := filepath.Join(dir, version, "firecracker")
	os.MkdirAll(filepath.Dir(dstPath), 0o755)

	if localPath != "" {
		return copyFile(dstPath, localPath, "Firecracker", 0o755)
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

func copyBinary(dst, srcPath, defaultSrc, name string) error {
	src := srcPath
	if src == "" {
		src, _ = filepath.Abs(defaultSrc)
	}

	srcInfo, err := os.Stat(src)
	if os.IsNotExist(err) {
		return fmt.Errorf("%s not found at %s", name, src)
	}
	if err != nil {
		return err
	}

	if dstInfo, err := os.Stat(dst); err == nil {
		if !dstInfo.ModTime().Before(srcInfo.ModTime()) {
			fmt.Printf("✓ %s up-to-date\n", name)

			return nil
		}
	}

	return copyFile(dst, src, name, 0o755)
}

func copyFile(dst, src, name string, perm os.FileMode) error {
	srcInfo, err := os.Stat(src)
	if os.IsNotExist(err) {
		return fmt.Errorf("%s not found at %s", name, src)
	}
	if err != nil {
		return err
	}

	if dstInfo, err := os.Stat(dst); err == nil {
		if !dstInfo.ModTime().Before(srcInfo.ModTime()) {
			fmt.Printf("✓ %s up-to-date\n", name)

			return nil
		}
	}

	fmt.Printf("📋 Copying %s from %s...\n", name, src)
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	os.MkdirAll(filepath.Dir(dst), 0o755)
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err == nil {
		fmt.Printf("✓ %s ready\n", name)
	}
	_ = srcInfo // suppress unused warning

	return err
}
