package smoketest_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldlog"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template/peerclient"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/tcpfirewall"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
)

const (
	baseImage = "ubuntu:22.04"
	proxyPort = 5009
)

// TestSmokeAllFCVersions builds a template and resumes from it for every
// Firecracker version in FirecrackerVersionMap. It requires root, Docker,
// the envd binary, KVM, NBD, and hugepages.
func TestSmokeAllFCVersions(t *testing.T) { //nolint:paralleltest // subtests share infra and must run sequentially
	checkPrerequisites(t)

	dataDir := t.TempDir()
	envdPath := findOrBuildEnvd(t)

	setupLocalDirs(t, dataDir)
	setupEnvVars(t, dataDir, envdPath)

	downloadKernel(t, dataDir)
	for _, fcVersion := range featureflags.FirecrackerVersionMap {
		downloadFC(t, dataDir, fcVersion)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()

	infra := newTestInfra(t, ctx)
	defer infra.close(ctx)

	for fcMajor, fcVersion := range featureflags.FirecrackerVersionMap { //nolint:paralleltest // sequential by design
		t.Run("fc-"+fcMajor, func(t *testing.T) {
			buildID := uuid.New().String()

			// Phase 1: create build
			t.Logf("creating build %s with FC %s", buildID, fcVersion)
			force := true
			_, err := infra.builder.Build(
				ctx,
				storage.TemplateFiles{BuildID: buildID},
				config.TemplateConfig{
					Version:            templates.TemplateV2LatestVersion,
					TemplateID:         "smoke-" + fcMajor,
					Force:              &force,
					VCpuCount:          2,
					MemoryMB:           512,
					DiskSizeMB:         512,
					HugePages:          true,
					KernelVersion:      featureflags.DefaultKernelVersion,
					FirecrackerVersion: fcVersion,
					FromImage:          baseImage,
				},
				logger.NewNopLogger().Detach(ctx).Core(),
			)
			require.NoError(t, err, "create build failed for FC %s", fcVersion)
			t.Logf("build %s done", buildID)

			// Phase 2: resume from the build
			t.Logf("resuming build %s", buildID)
			tmpl, err := infra.templateCache.GetTemplate(ctx, buildID, false, false)
			require.NoError(t, err, "load template for FC %s", fcVersion)

			meta, err := tmpl.Metadata()
			require.NoError(t, err)

			token := "smoke-test"
			t0 := time.Now()
			sbx, err := infra.factory.ResumeSandbox(
				ctx,
				tmpl,
				sandbox.Config{
					BaseTemplateID: "smoke-" + fcMajor,
					Vcpu:           2,
					RamMB:          512,
					HugePages:      true,
					Network:        &orchestrator.SandboxNetworkConfig{},
					Envd: sandbox.EnvdMetadata{
						Vars:        map[string]string{},
						AccessToken: &token,
						Version:     "1.0.0",
					},
					FirecrackerConfig: fc.Config{
						KernelVersion:      meta.Template.KernelVersion,
						FirecrackerVersion: meta.Template.FirecrackerVersion,
					},
				},
				sandbox.RuntimeMetadata{
					TemplateID:  "smoke-" + fcMajor,
					TeamID:      "smoke",
					SandboxID:   fmt.Sprintf("sbx-smoke-%s-%d", fcMajor, time.Now().UnixNano()),
					ExecutionID: uuid.NewString(),
				},
				t0,
				t0.Add(10*time.Minute),
				nil,
			)
			require.NoError(t, err, "resume failed for FC %s", fcVersion)
			t.Logf("resumed in %s", time.Since(t0))

			sbx.Close(context.WithoutCancel(ctx))
		})
	}
}

// testInfra holds the shared infrastructure used across all FC version sub-tests.
type testInfra struct {
	builder       *build.Builder
	factory       *sandbox.Factory
	templateCache *sbxtemplate.Cache

	// resources to close
	closers []func(context.Context)
}

func (ti *testInfra) close(ctx context.Context) {
	cleanCtx := context.WithoutCancel(ctx)
	for i := len(ti.closers) - 1; i >= 0; i-- {
		ti.closers[i](cleanCtx)
	}
}

func newTestInfra(t *testing.T, ctx context.Context) *testInfra {
	t.Helper()

	l := logger.NewNopLogger()
	sbxlogger.SetSandboxLoggerInternal(l)
	sbxlogger.SetSandboxLoggerExternal(l)

	flags, _ := featureflags.NewClientWithLogLevel(ldlog.Error)

	builderConfig, err := cfg.ParseBuilder()
	require.NoError(t, err)

	networkConfig, err := network.ParseConfig()
	require.NoError(t, err)

	orcConfig, err := cfg.Parse()
	require.NoError(t, err)

	ti := &testInfra{}

	// Storage
	persistenceTemplate, err := storage.GetStorageProvider(ctx, storage.TemplateStorageConfig)
	require.NoError(t, err)

	persistenceBuild, err := storage.GetStorageProvider(ctx, storage.BuildCacheStorageConfig)
	require.NoError(t, err)

	// NBD
	devicePool, err := nbd.NewDevicePool()
	require.NoError(t, err)
	go devicePool.Populate(ctx)
	ti.closers = append(ti.closers, func(ctx context.Context) { devicePool.Close(ctx) })

	// Network
	slotStorage, err := network.NewStorageLocal(ctx, networkConfig)
	require.NoError(t, err)
	networkPool := network.NewPool(8, 8, slotStorage, networkConfig)
	go networkPool.Populate(ctx)
	ti.closers = append(ti.closers, func(ctx context.Context) { networkPool.Close(ctx) })

	// Artifacts / Docker
	artifactRegistry, err := artifactsregistry.GetArtifactsRegistryProvider(ctx)
	require.NoError(t, err)

	dockerhubRepo, err := dockerhub.GetRemoteRepository(ctx)
	require.NoError(t, err)
	ti.closers = append(ti.closers, func(_ context.Context) { dockerhubRepo.Close() })

	// Template cache
	blockMetrics, _ := blockmetrics.NewMetrics(noop.NewMeterProvider())
	templateCache, err := sbxtemplate.NewCache(orcConfig, flags, persistenceTemplate, blockMetrics, peerclient.NopResolver())
	require.NoError(t, err)
	templateCache.Start(ctx)
	ti.closers = append(ti.closers, func(_ context.Context) { templateCache.Stop() })
	ti.templateCache = templateCache

	// Sandbox proxy + TCP firewall
	sandboxes := sandbox.NewSandboxesMap()

	sandboxProxy, err := proxy.NewSandboxProxy(noop.MeterProvider{}, proxyPort, sandboxes, flags)
	require.NoError(t, err)
	go sandboxProxy.Start(ctx)
	ti.closers = append(ti.closers, func(ctx context.Context) { sandboxProxy.Close(ctx) })

	tcpFw := tcpfirewall.New(l, networkConfig, sandboxes, noop.NewMeterProvider(), flags)
	go tcpFw.Start(ctx)
	ti.closers = append(ti.closers, func(ctx context.Context) { tcpFw.Close(ctx) })

	// Factory + Builder
	factory := sandbox.NewFactory(orcConfig.BuilderConfig, networkPool, devicePool, flags, nil, nil, sandboxes)
	ti.factory = factory

	buildMetrics, _ := metrics.NewBuildMetrics(noop.MeterProvider{})
	ti.builder = build.NewBuilder(
		builderConfig, l, flags, factory,
		persistenceTemplate, persistenceBuild, artifactRegistry,
		dockerhubRepo, sandboxProxy, sandboxes, templateCache, buildMetrics,
	)

	return ti
}

// --- prerequisites ----------------------------------------------------------

func checkPrerequisites(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}

	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("/dev/kvm not available")
	}
}

// --- envd -------------------------------------------------------------------

func findOrBuildEnvd(t *testing.T) string {
	t.Helper()

	if p := os.Getenv("HOST_ENVD_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			t.Logf("using envd from HOST_ENVD_PATH: %s", p)

			return p
		}
	}

	envdDir := locateEnvdSource(t)
	if envdDir == "" {
		t.Skip("cannot locate envd source directory")
	}

	binPath := filepath.Join(envdDir, "bin", "envd")
	if _, err := os.Stat(binPath); err == nil {
		t.Logf("using existing envd binary: %s", binPath)

		return binPath
	}

	t.Logf("building envd from %s", envdDir)
	require.NoError(t, os.MkdirAll(filepath.Join(envdDir, "bin"), 0o755))

	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", binPath, ".") //nolint:gosec // trusted input
	cmd.Dir = envdDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("failed to build envd: %v\n%s", err, out)
	}

	t.Logf("built envd: %s", binPath)

	return binPath
}

func locateEnvdSource(t *testing.T) string {
	t.Helper()

	// Walk up from the test directory to find packages/envd
	wd, err := os.Getwd()
	require.NoError(t, err)

	for dir := wd; dir != "/"; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "packages", "envd", "main.go")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Join(dir, "packages", "envd")
		}
	}

	return ""
}

// --- local storage setup ----------------------------------------------------

func setupLocalDirs(t *testing.T, dataDir string) {
	t.Helper()
	for _, d := range []string{"kernels", "templates", "sandbox", "orchestrator", "snapshot-cache", "fc-versions", "build-cache"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dataDir, d), 0o755))
	}
	for _, d := range []string{"build", "build-templates", "sandbox", "snapshot-cache", "template"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "orchestrator", d), 0o755))
	}
}

func setupEnvVars(t *testing.T, dataDir, envdPath string) {
	t.Helper()

	abs := func(rel string) string {
		a, err := filepath.Abs(rel)
		require.NoError(t, err)

		return a
	}

	vars := map[string]string{
		"ARTIFACTS_REGISTRY_PROVIDER":         "Local",
		"FIRECRACKER_VERSIONS_DIR":            abs(filepath.Join(dataDir, "fc-versions")),
		"HOST_ENVD_PATH":                      envdPath,
		"HOST_KERNELS_DIR":                    abs(filepath.Join(dataDir, "kernels")),
		"LOCAL_TEMPLATE_STORAGE_BASE_PATH":    abs(filepath.Join(dataDir, "templates")),
		"LOCAL_BUILD_CACHE_STORAGE_BASE_PATH": abs(filepath.Join(dataDir, "build-cache")),
		"ORCHESTRATOR_BASE_PATH":              abs(filepath.Join(dataDir, "orchestrator")),
		"SANDBOX_DIR":                         abs(filepath.Join(dataDir, "sandbox")),
		"SNAPSHOT_CACHE_DIR":                  abs(filepath.Join(dataDir, "snapshot-cache")),
		"STORAGE_PROVIDER":                    "Local",
		"USE_LOCAL_NAMESPACE_STORAGE":         "true",
	}

	for k, v := range vars {
		t.Setenv(k, v)
	}
}

// --- binary downloads -------------------------------------------------------

func downloadKernel(t *testing.T, dataDir string) {
	t.Helper()
	dst := filepath.Join(dataDir, "kernels", featureflags.DefaultKernelVersion, "vmlinux.bin")
	url := fmt.Sprintf("https://storage.googleapis.com/e2b-prod-public-builds/kernels/%s/vmlinux.bin", featureflags.DefaultKernelVersion)
	downloadFile(t, url, dst, 0o644)
}

func downloadFC(t *testing.T, dataDir, version string) {
	t.Helper()
	dst := filepath.Join(dataDir, "fc-versions", version, "firecracker")
	url := fmt.Sprintf("https://github.com/e2b-dev/fc-versions/releases/download/%s/firecracker", version)
	downloadFile(t, url, dst, 0o755)
}

func downloadFile(t *testing.T, url, dst string, perm os.FileMode) {
	t.Helper()

	if _, err := os.Stat(dst); err == nil {
		return
	}

	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))

	t.Logf("downloading %s", url)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "download %s", url)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "download %s returned HTTP %d", url, resp.StatusCode)

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	require.NoError(t, err)
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	require.NoError(t, err)
}
