package main

import (
	"net/http"
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

func TestBaseImageLaunch(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("skipping benchmark because not running as root")
	}

	baseImage := "e2bdev/base"
	kernelVersion := "vmlinux-6.1.102"
	fcVersion := "v1.10.1_1fcdaec08"

	tempDir := t.TempDir()

	abs := func(s string) string {
		return utils.Must(filepath.Abs(s))
	}

	// hacks, these should go away
	t.Setenv("USE_LOCAL_NAMESPACE_STORAGE", "true")
	t.Setenv("STORAGE_PROVIDER", "Local")
	t.Setenv("ORCHESTRATOR_BASE_PATH", tempDir)
	t.Setenv("HOST_ENVD_PATH", abs(filepath.Join("..", "envd", "bin", "envd")))
	t.Setenv("FIRECRACKER_VERSIONS_DIR", abs(filepath.Join("..", "fc-versions", "builds")))
	t.Setenv("HOST_KERNELS_DIR", abs(filepath.Join("..", "fc-kernels")))
	t.Setenv("SANDBOX_DIR", abs(filepath.Join(tempDir, "fc-vm")))
	t.Setenv("SNAPSHOT_CACHE_DIR", abs(filepath.Join(tempDir, "snapshot-cache")))
	t.Setenv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", abs(filepath.Join(tempDir, "templates")))

	// prep directories
	for _, subdir := range []string{"build", "build-templates" /*"fc-vm",*/, "sandbox", "snapshot-cache", "template"} {
		fullDirName := filepath.Join(tempDir, subdir)
		err := os.MkdirAll(fullDirName, 0755)
		require.NoError(t, err)
	}

	clientID := uuid.NewString()

	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	sbxlogger.SetSandboxLoggerInternal(logger)
	//sbxlogger.SetSandboxLoggerExternal(logger)

	networkPool, err := network.NewPool(
		t.Context(), noop.MeterProvider{}, 8, 8, clientID,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := networkPool.Close(t.Context())
		assert.NoError(t, err)
	})

	devicePool, err := nbd.NewDevicePool(t.Context(), noop.MeterProvider{})
	require.NoError(t, err, "do you have the nbd kernel module installed?")
	t.Cleanup(func() {
		err := devicePool.Close(t.Context())
		assert.NoError(t, err)
	})

	featureFlags, err := featureflags.NewClient()
	require.NoError(t, err)
	t.Cleanup(func() {
		err := featureFlags.Close(t.Context())
		assert.NoError(t, err)
	})

	limiter, err := limit.New(t.Context(), featureFlags)
	require.NoError(t, err)

	persistence, err := storage.GetTemplateStorageProvider(t.Context(), limiter)
	require.NoError(t, err)

	blockMetrics, err := blockmetrics.NewMetrics(&noop.MeterProvider{})
	require.NoError(t, err)

	templateCache, err := template.NewCache(t.Context(), featureFlags, persistence, blockMetrics)
	require.NoError(t, err)

	allowInternetAccess := true
	accessToken := "access-token"
	sandboxConfig := sandbox.Config{
		BaseTemplateID:      "base-template-id",
		Vcpu:                2,
		RamMB:               512,
		TotalDiskSizeMB:     2 * 1024,
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
	require.NoError(t, err)

	persistenceTemplate, err := storage.GetTemplateStorageProvider(t.Context(), nil)
	require.NoError(t, err)

	persistenceBuild, err := storage.GetBuildCacheStorageProvider(t.Context(), nil)
	require.NoError(t, err)

	var proxyPort uint = 5007

	sandboxes := smap.New[*sandbox.Sandbox]()

	sandboxProxy, err := proxy.NewSandboxProxy(noop.MeterProvider{}, proxyPort, sandboxes)
	require.NoError(t, err)
	go func() {
		err := sandboxProxy.Start(t.Context())
		assert.ErrorIs(t, http.ErrServerClosed, err)
	}()
	t.Cleanup(func() {
		err := sandboxProxy.Close(t.Context())
		assert.NoError(t, err)
	})

	buildMetrics, err := metrics.NewBuildMetrics(noop.MeterProvider{})
	require.NoError(t, err)

	templateID := "fcb33d09-3141-42c4-8d3b-c2df411681db"
	buildID := "ba6aae36-74f7-487a-b6f7-74fd7c94e479"

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
	_, err = builder.Build(t.Context(), metadata, templateConfig, logger.Core())
	require.NoError(t, err)

	// retrieve template
	tmpl, err := templateCache.GetTemplate(
		t.Context(),
		buildID,
		kernelVersion,
		fcVersion,
		false,
		false,
	)
	require.NoError(t, err)

	// create sandbox
	sbx, err := sandbox.ResumeSandbox(
		t.Context(),
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
	require.NoError(t, err)

	// pause sandbox
	// build base template
	meta, err := sbx.Template.Metadata()
	require.NoError(t, err)

	templateMetadata := meta.SameVersionTemplate(storage.TemplateFiles{
		BuildID:            buildID,
		KernelVersion:      kernelVersion,
		FirecrackerVersion: fcVersion,
	})
	snap, err := sbx.Pause(t.Context(), templateMetadata)
	require.NoError(t, err)
	require.NotNil(t, snap)

	// resume sandbox
	sbx, err = sandbox.ResumeSandbox(t.Context(), networkPool, tmpl, sandboxConfig, runtime, uuid.NewString(), time.Now(), time.Now().Add(time.Second*15), devicePool, false, nil)
	require.NoError(t, err)

	// close sandbox
	err = sbx.Close(t.Context())
	require.NoError(t, err)
}
