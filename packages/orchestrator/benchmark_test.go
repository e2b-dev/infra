// run with something like:
//
// sudo modprobe nbd
// sudo `which go` test ./packages/orchestrator/ -bench=BenchmarkBaseImage -v -timeout=60m
//
// Single mode:
//
// sudo `which go` test ./packages/orchestrator/ -bench=BenchmarkBaseImage/zstd-2 -v
//
// More iterations:
//
// sudo `which go` test ./packages/orchestrator/ -bench=BenchmarkBaseImage -benchtime=5x -v -timeout=60m
package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/tcpfirewall"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	buildconfig "github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type benchMode struct {
	name            string
	buildID         string
	compressionType string // "lz4" or "zstd"; "" = uncompressed
	level           int
}

func (m benchMode) compressed() bool { return m.compressionType != "" }

var benchModes = []benchMode{
	{"uncompressed", "ba6aae36-0000-0000-0000-000000000000", "", 0},
	{"lz4", "ba6aae36-0000-0000-0000-000000000001", "lz4", 0},
	{"zstd-0", "ba6aae36-0000-0000-0000-000000000002", "zstd", 0},
	{"zstd-1", "ba6aae36-0000-0000-0000-000000000003", "zstd", 1},
	{"zstd-2", "ba6aae36-0000-0000-0000-000000000004", "zstd", 2},
	{"zstd-3", "ba6aae36-0000-0000-0000-000000000005", "zstd", 3},
}

func BenchmarkBaseImage(b *testing.B) {
	if os.Geteuid() != 0 {
		b.Skip("skipping benchmark because not running as root")
	}

	const (
		baseImage       = "e2bdev/base"
		kernelVersion   = "vmlinux-6.1.158"
		fcVersion       = featureflags.DefaultFirecrackerVersion
		templateID      = "fcb33d09-3141-42c4-8d3b-c2df411681db"
		useHugePages    = false
		templateVersion = "v2.0.0"
	)

	sbxNetwork := &orchestrator.SandboxNetworkConfig{}

	// cache paths, to speed up test runs. these paths aren't wiped between tests
	persistenceDir := getPersistenceDir()
	kernelsDir := filepath.Join(persistenceDir, "kernels")
	sandboxDir := filepath.Join(persistenceDir, "sandbox")
	err := os.MkdirAll(kernelsDir, 0o755)
	require.NoError(b, err)

	// ephemeral data
	tempDir := b.TempDir()

	abs := func(s string) string {
		return utils.Must(filepath.Abs(s))
	}

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint != "" {
		spanExporter, err := telemetry.NewSpanExporter(b.Context(),
			otlptracegrpc.WithEndpoint(endpoint),
		)
		b.Cleanup(func() {
			ctx := context.WithoutCancel(b.Context())
			err := spanExporter.Shutdown(ctx)
			assert.NoError(b, err)
		})

		require.NoError(b, err)
		resource, err := telemetry.GetResource(b.Context(), "node-id", "BenchmarkBaseImage", "service-commit", "service-version", "service-instance-id")
		require.NoError(b, err)
		tracerProvider := telemetry.NewTracerProvider(spanExporter, resource)
		otel.SetTracerProvider(tracerProvider)
	}

	linuxKernelURL, err := url.JoinPath("https://storage.googleapis.com/e2b-prod-public-builds/kernels/", kernelVersion, "vmlinux.bin")
	require.NoError(b, err)
	linuxKernelFilename := filepath.Join(kernelsDir, kernelVersion, "vmlinux.bin")

	downloadKernel(b, linuxKernelFilename, linuxKernelURL)

	// hacks, these should go away
	templateStoragePath := abs(filepath.Join(persistenceDir, "templates"))
	b.Setenv("ARTIFACTS_REGISTRY_PROVIDER", "Local")
	b.Setenv("FIRECRACKER_VERSIONS_DIR", abs(filepath.Join("..", "fc-versions", "builds")))
	b.Setenv("HOST_ENVD_PATH", abs(filepath.Join("..", "envd", "bin", "envd")))
	b.Setenv("HOST_KERNELS_DIR", abs(kernelsDir))
	b.Setenv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", templateStoragePath)
	b.Setenv("ORCHESTRATOR_BASE_PATH", tempDir)
	b.Setenv("SANDBOX_DIR", abs(sandboxDir))
	b.Setenv("SNAPSHOT_CACHE_DIR", abs(filepath.Join(tempDir, "snapshot-cache")))
	b.Setenv("STORAGE_PROVIDER", "Local")
	b.Setenv("USE_LOCAL_NAMESPACE_STORAGE", "true")

	config, err := cfg.Parse()
	require.NoError(b, err)

	// prep directories
	for _, subdir := range []string{"build", "build-templates" /*"fc-vm",*/, "sandbox", "snapshot-cache", "template"} {
		fullDirName := filepath.Join(tempDir, subdir)
		err := os.MkdirAll(fullDirName, 0o755)
		require.NoError(b, err)
	}

	l, err := logger.NewDevelopmentLogger()
	require.NoError(b, err)

	sbxlogger.SetSandboxLoggerInternal(l)

	slotStorage, err := network.NewStorageLocal(b.Context(), config.NetworkConfig)
	require.NoError(b, err)
	networkPool := network.NewPool(8, 8, slotStorage, config.NetworkConfig)
	go func() {
		networkPool.Populate(b.Context())
		l.Info(b.Context(), "network pool populated")
	}()
	b.Cleanup(func() {
		ctx := context.WithoutCancel(b.Context())
		err := networkPool.Close(ctx)
		assert.NoError(b, err)
	})

	devicePool, err := nbd.NewDevicePool()
	require.NoError(b, err, "do you have the nbd kernel module installed?")
	go func() {
		devicePool.Populate(b.Context())
		l.Info(b.Context(), "device pool populated")
	}()
	b.Cleanup(func() {
		ctx := context.WithoutCancel(b.Context())
		err := devicePool.Close(ctx)
		assert.NoError(b, err)
	})

	featureFlags, err := featureflags.NewClient()
	require.NoError(b, err)
	b.Cleanup(func() {
		ctx := context.WithoutCancel(b.Context())
		err := featureFlags.Close(ctx)
		assert.NoError(b, err)
	})

	limiter, err := limit.New(b.Context(), featureFlags)
	require.NoError(b, err)

	persistence, err := storage.GetTemplateStorageProvider(b.Context(), limiter)
	require.NoError(b, err)

	blockMetrics, err := blockmetrics.NewMetrics(&noop.MeterProvider{})
	require.NoError(b, err)

	c, err := cfg.Parse()
	require.NoError(b, err)

	templateCache, err := template.NewCache(c, featureFlags, persistence, blockMetrics)
	require.NoError(b, err)
	templateCache.Start(b.Context())
	b.Cleanup(templateCache.Stop)

	sandboxFactory := sandbox.NewFactory(config.BuilderConfig, networkPool, devicePool, featureFlags, nil, nil)

	dockerhubRepository, err := dockerhub.GetRemoteRepository(b.Context())
	require.NoError(b, err)
	b.Cleanup(func() {
		err := dockerhubRepository.Close()
		assert.NoError(b, err)
	})

	accessToken := "access-token"
	sandboxConfig := sandbox.Config{
		BaseTemplateID:  templateID,
		Vcpu:            2,
		RamMB:           512,
		TotalDiskSizeMB: 2 * 1024,
		HugePages:       useHugePages,
		Network:         sbxNetwork,
		Envd: sandbox.EnvdMetadata{
			Vars:        map[string]string{"HELLO": "WORLD"},
			AccessToken: &accessToken,
			Version:     "1.2.3",
		},
		FirecrackerConfig: fc.Config{
			KernelVersion:      kernelVersion,
			FirecrackerVersion: fcVersion,
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

	tcpFirewall := tcpfirewall.New(
		l,
		config.NetworkConfig,
		sandboxes,
		noop.NewMeterProvider(),
		featureFlags,
	)
	go func() {
		err := tcpFirewall.Start(b.Context())
		assert.NoError(b, err)
	}()
	b.Cleanup(func() {
		ctx := context.WithoutCancel(b.Context())
		err := tcpFirewall.Close(ctx)
		assert.NoError(b, err)
	})

	sandboxProxy, err := proxy.NewSandboxProxy(noop.MeterProvider{}, proxyPort, sandboxes, featureFlags)
	require.NoError(b, err)
	go func() {
		err := sandboxProxy.Start(b.Context())
		assert.ErrorIs(b, http.ErrServerClosed, err)
	}()
	b.Cleanup(func() {
		ctx := context.WithoutCancel(b.Context())
		err := sandboxProxy.Close(ctx)
		assert.NoError(b, err)
	})

	buildMetrics, err := metrics.NewBuildMetrics(noop.MeterProvider{})
	require.NoError(b, err)

	builder := build.NewBuilder(
		config.BuilderConfig,
		l,
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

	force := true
	templateConfig := buildconfig.TemplateConfig{
		Version:            templateVersion,
		TemplateID:         templateID,
		FromImage:          baseImage,
		Force:              &force,
		VCpuCount:          sandboxConfig.Vcpu,
		MemoryMB:           sandboxConfig.RamMB,
		StartCmd:           "echo 'start cmd debug' && sleep .1 && echo 'done starting command debug'",
		DiskSizeMB:         sandboxConfig.TotalDiskSizeMB,
		HugePages:          sandboxConfig.HugePages,
		KernelVersion:      kernelVersion,
		FirecrackerVersion: fcVersion,
	}

	for _, mode := range benchModes {
		b.Run(mode.name, func(b *testing.B) {
			// Set flags for this mode
			featureflags.OverrideJSONFlag(featureflags.CompressConfigFlag, ldvalue.FromJSONMarshal(map[string]any{
				"compressBuilds":     mode.compressed(),
				"compressionType":    mode.compressionType,
				"level":              mode.level,
				"frameSizeKB":        2048,
				"uploadPartTargetMB": 50,
				"encodeWorkers":      4,
				"encoderConcurrency": 1,
				"decoderConcurrency": 1,
			}))

			b.Logf("mode=%s buildID=%s compressed=%v type=%s level=%d",
				mode.name, mode.buildID, mode.compressed(), mode.compressionType, mode.level)

			// Build (exactly once, timed for reporting).
			// Skipped if template already exists on disk.
			// To force rebuild: rm -rf /root/.cache/e2b-orchestrator-benchmark/templates/
			buildStart := time.Now()
			buildPath := filepath.Join(templateStoragePath, mode.buildID, "rootfs.ext4")
			if _, err := os.Stat(buildPath); os.IsNotExist(err) {
				metadata := storage.TemplateFiles{BuildID: mode.buildID}
				_, err = builder.Build(b.Context(), metadata, templateConfig, l.Detach(b.Context()).Core())
				require.NoError(b, err)
			}
			buildDuration := time.Since(buildStart)

			// Cold start benchmark.
			// Each iteration gets a fresh template with empty block caches.
			// InvalidateAll() evicts the cached template; GetTemplate() creates
			// a new storageTemplate with fresh chunkers (no mmap data cached).
			// Template headers reload from local FS (cheap, OS page cache).
			// The timed ResumeSandbox() then triggers real block fetches on
			// every page fault — a true cold start.
			b.ResetTimer()
			b.StopTimer()
			for range b.N {
				// Setup (untimed): fresh template with empty block cache
				templateCache.InvalidateAll()
				tmpl, err := templateCache.GetTemplate(b.Context(), mode.buildID, false, false)
				require.NoError(b, err)

				_, err = tmpl.Metadata()
				require.NoError(b, err)

				// Timed: cold start sandbox launch
				b.StartTimer()
				sbx, err := sandboxFactory.ResumeSandbox(
					b.Context(),
					tmpl,
					sandboxConfig,
					runtime,
					time.Now(),
					time.Now().Add(time.Second*15),
					nil,
				)
				b.StopTimer()
				require.NoError(b, err)

				// Cleanup (untimed)
				err = sbx.Close(b.Context())
				require.NoError(b, err)
			}

			b.ReportMetric(buildDuration.Seconds(), "build-s")
		})
	}
}

func getPersistenceDir() string {
	home := os.Getenv("HOME")
	if home != "" {
		return filepath.Join(home, ".cache", "e2b-orchestrator-benchmark")
	}

	return filepath.Join(os.TempDir(), "e2b-orchestrator-benchmark")
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
