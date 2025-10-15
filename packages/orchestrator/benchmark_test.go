// run with something like:
//
// sudo `which go` test -benchtime=15s -bench=. -v
// sudo modprobe nbd
// echo 1024 | sudo tee /proc/sys/vm/nr_hugepages
package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator")

func BenchmarkBaseImageLaunch(b *testing.B) {
	if os.Geteuid() != 0 {
		b.Skip("skipping benchmark because not running as root")
	}

	// test configuration
	const (
		testType            = onlyStart
		baseImage           = "e2bdev/base"
		kernelVersion       = "vmlinux-6.1.102"
		fcVersion           = "v1.10.1_1fcdaec08"
		templateID          = "fcb33d09-3141-42c4-8d3b-c2df411681db"
		buildID             = "ba6aae36-74f7-487a-b6f7-74fd7c94e479"
		useHugePages        = false
		allowInternetAccess = true
		templateVersion     = "v2.0.0"
	)

	// cache paths, to speed up test runs. these paths aren't wiped between tests
	persistenceDir := filepath.Join(os.TempDir(), "e2b-orchestrator-benchmark")
	kernelsDir := filepath.Join(persistenceDir, "kernels")
	sandboxDir := filepath.Join(persistenceDir, "sandbox")
	err := os.MkdirAll(kernelsDir, 0o755)
	require.NoError(b, err)

	// ephemeral data
	tempDir := b.TempDir()
	clientID := uuid.NewString()

	abs := func(s string) string {
		return utils.Must(filepath.Abs(s))
	}

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint != "" {
		spanExporter, err := telemetry.NewSpanExporter(b.Context(),
			otlptracegrpc.WithEndpoint(endpoint),
		)
		defer func() {
			err := spanExporter.Shutdown(b.Context())
			assert.NoError(b, err)
		}()
		require.NoError(b, err)
		resource, err := telemetry.GetResource(b.Context(), "node-id", "BenchmarkBaseImageLaunch", "service-commit", "service-version", "service-instance-id")
		require.NoError(b, err)
		tracerProvider := telemetry.NewTracerProvider(spanExporter, resource)
		otel.SetTracerProvider(tracerProvider)
	}

	linuxKernelURL, err := url.JoinPath("https://storage.googleapis.com/e2b-prod-public-builds/kernels/", kernelVersion, "vmlinux.bin")
	require.NoError(b, err)
	linuxKernelFilename := filepath.Join(kernelsDir, kernelVersion, "vmlinux.bin")

	downloadKernel(b, linuxKernelFilename, linuxKernelURL)

	// hacks, these should go away
	b.Setenv("ARTIFACTS_REGISTRY_PROVIDER", "Local")
	b.Setenv("USE_LOCAL_NAMESPACE_STORAGE", "true")
	b.Setenv("STORAGE_PROVIDER", "Local")
	b.Setenv("ORCHESTRATOR_BASE_PATH", tempDir)
	b.Setenv("HOST_ENVD_PATH", abs(filepath.Join("..", "envd", "bin", "envd")))
	b.Setenv("FIRECRACKER_VERSIONS_DIR", abs(filepath.Join("..", "fc-versions", "builds")))
	b.Setenv("HOST_KERNELS_DIR", abs(kernelsDir))
	b.Setenv("SANDBOX_DIR", abs(sandboxDir))
	b.Setenv("SNAPSHOT_CACHE_DIR", abs(filepath.Join(tempDir, "snapshot-cache")))
	b.Setenv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", abs(filepath.Join(persistenceDir, "templates")))

	networkConfig, err := network.ParseConfig()
	if err != nil {
		b.Fatalf("error parsing config: %v", err)
	}

	// prep directories
	for _, subdir := range []string{"build", "build-templates" /*"fc-vm",*/, "sandbox", "snapshot-cache", "template"} {
		fullDirName := filepath.Join(tempDir, subdir)
		err := os.MkdirAll(fullDirName, 0o755)
		require.NoError(b, err)
	}

	logger, err := zap.NewDevelopment()
	require.NoError(b, err)

	sbxlogger.SetSandboxLoggerInternal(logger)
	// sbxlogger.SetSandboxLoggerExternal(logger)

	networkPool, err := network.NewPool(noop.MeterProvider{}, 8, 8, clientID, networkConfig)
	require.NoError(b, err)
	go func() {
		networkPool.Populate(b.Context())
		logger.Info("network pool populated")
	}()
	defer func() {
		err := networkPool.Close(b.Context())
		assert.NoError(b, err)
	}()

	devicePool, err := nbd.NewDevicePool(noop.MeterProvider{})
	require.NoError(b, err, "do you have the nbd kernel module installed?")
	go func() {
		devicePool.Populate(b.Context())
		logger.Info("device pool populated")
	}()
	defer func() {
		err := devicePool.Close(b.Context())
		assert.NoError(b, err)
	}()

	featureFlags, err := featureflags.NewClient()
	require.NoError(b, err)
	defer func() {
		err := featureFlags.Close(b.Context())
		assert.NoError(b, err)
	}()

	limiter, err := limit.New(b.Context(), featureFlags)
	require.NoError(b, err)

	persistence, err := storage.GetTemplateStorageProvider(b.Context(), limiter)
	require.NoError(b, err)

	blockMetrics, err := blockmetrics.NewMetrics(&noop.MeterProvider{})
	require.NoError(b, err)

	c, err := cfg.Parse()
	if err != nil {
		b.Fatalf("error parsing config: %v", err)
	}

	templateCache, err := template.NewCache(b.Context(), c, featureFlags, persistence, blockMetrics)
	require.NoError(b, err)

	sandboxFactory := sandbox.NewFactory(networkPool, devicePool, featureFlags, true)

	dockerhubRepository, err := dockerhub.GetRemoteRepository(b.Context())
	require.NoError(b, err)
	defer func() {
		err := dockerhubRepository.Close()
		assert.NoError(b, err)
	}()

	accessToken := "access-token"
	sandboxConfig := sandbox.Config{
		BaseTemplateID:      templateID,
		Vcpu:                2,
		RamMB:               512,
		TotalDiskSizeMB:     2 * 1024,
		HugePages:           useHugePages,
		AllowInternetAccess: ptr(allowInternetAccess),
		Envd: sandbox.EnvdMetadata{
			Vars:        map[string]string{"HELLO": "WORLD"},
			AccessToken: &accessToken,
			Version:     "1.2.3",
		},
	}

	runtime := sandbox.RuntimeMetadata{
		TemplateID:  templateID,
		SandboxID:   "sandbox-id",
		ExecutionID: "execution-id",
		TeamID:      "team-id",
	}

	artifactRegistry, err := artifactsregistry.GetArtifactsRegistryProvider(b.Context())
	require.NoError(b, err)

	persistenceTemplate, err := storage.GetTemplateStorageProvider(b.Context(), nil)
	require.NoError(b, err)

	persistenceBuild, err := storage.GetBuildCacheStorageProvider(b.Context(), nil)
	require.NoError(b, err)

	var proxyPort uint16 = 5007

	sandboxes := sandbox.NewSandboxesMap()

	sandboxProxy, err := proxy.NewSandboxProxy(noop.MeterProvider{}, proxyPort, sandboxes)
	require.NoError(b, err)
	go func() {
		err := sandboxProxy.Start(b.Context())
		assert.ErrorIs(b, http.ErrServerClosed, err)
	}()
	defer func() {
		err := sandboxProxy.Close(b.Context())
		assert.NoError(b, err)
	}()

	buildMetrics, err := metrics.NewBuildMetrics(noop.MeterProvider{})
	require.NoError(b, err)

	builder := build.NewBuilder(
		logger,
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

	buildPath := filepath.Join(os.Getenv("LOCAL_TEMPLATE_STORAGE_BASE_PATH"), buildID, "rootfs.ext4")
	if _, err := os.Stat(buildPath); os.IsNotExist(err) {
		// build template
		force := true
		templateConfig := config.TemplateConfig{
			Version:    templateVersion,
			TemplateID: templateID,
			FromImage:  baseImage,
			Force:      &force,
			VCpuCount:  sandboxConfig.Vcpu,
			MemoryMB:   sandboxConfig.RamMB,
			StartCmd:   "echo 'start cmd debug' && sleep 10 && echo 'done starting command debug'",
			DiskSizeMB: sandboxConfig.TotalDiskSizeMB,
			HugePages:  sandboxConfig.HugePages,
		}

		metadata := storage.TemplateFiles{
			BuildID:            buildID,
			KernelVersion:      kernelVersion,
			FirecrackerVersion: fcVersion,
		}
		_, err = builder.Build(b.Context(), metadata, templateConfig, logger.Core())
		require.NoError(b, err)
	}

	// retrieve template
	tmpl, err := templateCache.GetTemplate(
		b.Context(),
		buildID,
		kernelVersion,
		fcVersion,
		false,
		false,
	)
	require.NoError(b, err)

	tc := testContainer{
		sandboxFactory: sandboxFactory,
		testType:       testType,
		tmpl:           tmpl,
		sandboxConfig:  sandboxConfig,
		runtime:        runtime,
	}

	for b.Loop() {
		tc.testOneItem(b, buildID, kernelVersion, fcVersion)
	}
}

func ptr[T any](v T) *T {
	return &v
}

type testCycle string

const (
	onlyStart        testCycle = "only-start"
	startAndPause    testCycle = "start-and-pause"
	startPauseResume testCycle = "start-pause-resume"
)

type testContainer struct {
	testType       testCycle
	sandboxFactory *sandbox.Factory
	tmpl           template.Template
	sandboxConfig  sandbox.Config
	runtime        sandbox.RuntimeMetadata
}

func (tc *testContainer) testOneItem(b *testing.B, buildID, kernelVersion, fcVersion string) {
	b.Helper()

	ctx, span := tracer.Start(b.Context(), "testOneItem")
	defer span.End()

	sbx, err := tc.sandboxFactory.ResumeSandbox(
		ctx,
		tc.tmpl,
		tc.sandboxConfig,
		tc.runtime,
		time.Now(),
		time.Now().Add(time.Second*15),
		nil,
	)
	require.NoError(b, err)

	if tc.testType == onlyStart {
		b.StopTimer()
		err = sbx.Close(ctx)
		require.NoError(b, err)
		b.StartTimer()
		return
	}

	meta, err := sbx.Template.Metadata()
	require.NoError(b, err)

	templateMetadata := meta.SameVersionTemplate(storage.TemplateFiles{
		BuildID:            buildID,
		KernelVersion:      kernelVersion,
		FirecrackerVersion: fcVersion,
	})
	snap, err := sbx.Pause(ctx, templateMetadata)
	require.NoError(b, err)
	require.NotNil(b, snap)

	if tc.testType == startAndPause {
		b.StopTimer()
		err = sbx.Close(ctx)
		require.NoError(b, err)
		b.StartTimer()
	}

	// resume sandbox
	sbx, err = tc.sandboxFactory.ResumeSandbox(ctx, tc.tmpl, tc.sandboxConfig, tc.runtime, time.Now(), time.Now().Add(time.Second*15), nil)
	require.NoError(b, err)

	// close sandbox
	err = sbx.Close(ctx)
	require.NoError(b, err)
}

func downloadKernel(b *testing.B, filename, url string) {
	b.Helper()

	dirname := filepath.Dir(filename)
	err := os.MkdirAll(dirname, 0o755)
	require.NoError(b, err)

	// kernel already exists
	if _, err := os.Stat(filename); err == nil {
		return
	}

	client := &http.Client{}
	req, err := http.NewRequestWithContext(b.Context(), http.MethodGet, url, nil)
	require.NoError(b, err)
	response, err := client.Do(req)
	require.NoError(b, err)
	require.Equal(b, http.StatusOK, response.StatusCode)
	defer response.Body.Close()

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(b, err)
	defer file.Close()

	_, err = file.ReadFrom(response.Body)
	require.NoError(b, err)
}
