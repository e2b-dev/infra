package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/zap"
)

func BenchmarkBaseImageLaunch(b *testing.B) {
	baseImage := "e2bdev/base"
	kernelVersion := "vmlinux-6.1.102"
	fcVersion := "v1.10.1_1fcdaec08"

	tempDir := b.TempDir()

	abs := func(s string) string {
		return utils.Must(filepath.Abs(s))
	}

	// hacks, these should go away
	b.Setenv("USE_LOCAL_NAMESPACE_STORAGE", "true")
	b.Setenv("STORAGE_PROVIDER", "Local")
	b.Setenv("ORCHESTRATOR_BASE_PATH", tempDir)
	b.Setenv("HOST_ENVD_PATH", abs(filepath.Join("..", "envd", "bin", "envd")))
	b.Setenv("FIRECRACKER_VERSIONS_DIR", abs(filepath.Join("..", "fc-versions", "builds")))
	b.Setenv("HOST_KERNELS_DIR", abs(filepath.Join("..", "fc-kernels")))
	b.Setenv("SANDBOX_DIR", abs(filepath.Join(tempDir, "fc-vm")))
	b.Setenv("SNAPSHOT_CACHE_DIR", abs(filepath.Join(tempDir, "snapshot-cache")))

	// prep directories
	for _, subdir := range []string{"build", "build-templates" /*"fc-vm",*/, "sandbox", "snapshot-cache", "template"} {
		fullDirName := filepath.Join(tempDir, subdir)
		err := os.MkdirAll(fullDirName, 0755)
		require.NoError(b, err)
	}

	clientID := uuid.NewString()

	logger, err := zap.NewDevelopment()
	require.NoError(b, err)

	sbxlogger.SetSandboxLoggerInternal(logger)
	//sbxlogger.SetSandboxLoggerExternal(logger)

	networkPool, err := network.NewPool(
		b.Context(), noop.MeterProvider{}, 8, 8, clientID,
	)
	require.NoError(b, err)
	b.Cleanup(func() {
		err := networkPool.Close(b.Context())
		assert.NoError(b, err)
	})

	devicePool, err := nbd.NewDevicePool(b.Context(), noop.MeterProvider{})
	require.NoError(b, err, "do you have the nbd kernel module installed?")
	b.Cleanup(func() {
		err := devicePool.Close(b.Context())
		assert.NoError(b, err)
	})

	featureFlags, err := featureflags.NewClient()
	require.NoError(b, err)
	b.Cleanup(func() {
		err := featureFlags.Close(b.Context())
		assert.NoError(b, err)
	})

	limiter, err := limit.New(b.Context(), featureFlags)
	require.NoError(b, err)

	persistence, err := storage.GetTemplateStorageProvider(b.Context(), limiter)
	require.NoError(b, err)

	blockMetrics, err := blockmetrics.NewMetrics(&noop.MeterProvider{})
	require.NoError(b, err)

	templateCache, err := template.NewCache(b.Context(), featureFlags, persistence, blockMetrics)
	require.NoError(b, err)

	allowInternetAccess := true
	accessToken := "access-token"
	sandboxConfig := sandbox.Config{
		BaseTemplateID:      "base-template-id",
		Vcpu:                1,
		RamMB:               2,
		TotalDiskSizeMB:     400,
		HugePages:           false,
		AllowInternetAccess: &allowInternetAccess,
		Envd: sandbox.EnvdMetadata{
			Vars:        map[string]string{"HELLO": "WORLD"},
			AccessToken: &accessToken,
			Version:     "1.2.3",
		},
	}

	runtime := sandbox.RuntimeMetadata{
		TemplateID:  "template-id",
		SandboxID:   "sandbox-id",
		ExecutionID: "execution-id",
		TeamID:      "team-id",
	}

	artifactRegistry, err := artifactsregistry.GetArtifactsRegistryProvider()
	require.NoError(b, err)

	persistenceTemplate, err := storage.GetTemplateStorageProvider(b.Context(), nil)
	require.NoError(b, err)

	persistenceBuild, err := storage.GetBuildCacheStorageProvider(b.Context(), nil)
	require.NoError(b, err)

	var proxyPort uint = 5007

	sandboxes := smap.New[*sandbox.Sandbox]()

	sandboxProxy, err := proxy.NewSandboxProxy(noop.MeterProvider{}, proxyPort, sandboxes)
	require.NoError(b, err)
	go func() {
		err := sandboxProxy.Start(b.Context())
		assert.NoError(b, err)
	}()
	b.Cleanup(func() {
		err := sandboxProxy.Close(b.Context())
		assert.NoError(b, err)
	})

	buildMetrics, err := metrics.NewBuildMetrics(noop.MeterProvider{})
	require.NoError(b, err)

	for b.Loop() {
		templateID := uuid.NewString()
		buildID := uuid.NewString()

		builder := build.NewBuilder(
			logger,
			persistenceTemplate,
			persistenceBuild,
			artifactRegistry,
			devicePool,
			networkPool,
			sandboxProxy,
			sandboxes,
			templateCache,
			buildMetrics,
		)

		// build template
		force := true
		templateConfig := config.TemplateConfig{
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
		_, err := builder.Build(b.Context(), metadata, templateConfig, logger.Core())
		require.NoError(b, err)

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

		// create sandbox
		sbx, err := sandbox.ResumeSandbox(
			b.Context(),
			networkPool,
			tmpl,
			sandboxConfig,
			runtime,
			uuid.NewString(),
			time.Now(),
			time.Now().Add(time.Second*15),
			devicePool,
			false,
			nil,
		)
		require.NoError(b, err)

		//// pause sandbox
		//// build base template
		//templateMetadata := tmpl.SameVersionTemplate(storage.TemplateFiles{
		//	BuildID:            "build-id",
		//	KernelVersion:      "kernel-version",
		//	FirecrackerVersion: "firecracker-version",
		//})
		//snap, err := sbx.Pause(b.Context(), templateMetadata)
		//require.NoError(b, err)
		//require.NotNil(b, snap)

		// resume sandbox

		// close sandbox
		err = sbx.Close(b.Context())
		require.NoError(b, err)
	}
}
