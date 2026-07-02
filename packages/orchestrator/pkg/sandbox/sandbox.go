//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/hoststats"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/cgroup"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/prefetch"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/scheduling"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	meter                         = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox")
	envdInitCalls                 = utils.Must(telemetry.GetCounter(meter, telemetry.EnvdInitCalls))
	waitForEnvdDurationHistogram  = utils.Must(telemetry.GetHistogram(meter, telemetry.WaitForEnvdDurationHistogramName))
	envdCollapseDurationHistogram = utils.Must(telemetry.GetHistogram(meter, telemetry.EnvdCollapseDurationHistogramName))
	envdCollapseChunks            = utils.Must(telemetry.GetCounter(meter, telemetry.EnvdCollapseChunks))
	guestSyncDurationHistogram    = utils.Must(telemetry.GetHistogram(meter, telemetry.GuestSyncDurationHistogramName))

	uffdStartupPagesHistogram       = utils.Must(telemetry.GetHistogram(meter, telemetry.UffdStartupPagesHistogramName))
	uffdStartupSourcePagesHistogram = utils.Must(telemetry.GetHistogram(meter, telemetry.UffdStartupSourcePagesHistogramName))
	uffdStartupBytesHistogram       = utils.Must(telemetry.GetHistogram(meter, telemetry.UffdStartupBytesHistogramName))
)

// Sandbox start types recorded on sandbox start/init metrics via the
// start_type attribute.
type StartType string

const (
	StartTypeCreate StartType = "create" // cold boot (template build)
	StartTypeResume StartType = "resume" // resume from a snapshot (the common runtime path)
	StartTypeReboot StartType = "reboot" // cold boot from a snapshot rootfs (filesystem-only resume)
)

// ErrWaitForEnvdTimeout is the cancel cause used when WaitForEnvd exceeds its timeout.
var ErrWaitForEnvdTimeout = errors.New("syncing took too long")

// ErrFcProcessExited is the cancel cause used when the Firecracker process exits during WaitForEnvd.
var ErrFcProcessExited = errors.New("fc process exited prematurely")

var SandboxHttpTransport = otelhttp.NewTransport(
	&http.Transport{
		DisableKeepAlives: true,
		ForceAttemptHTTP2: false,
	},
)

// Http client that should be used for requests to sandboxes.
var sandboxHttpClient = http.Client{
	Timeout:   10 * time.Second,
	Transport: SandboxHttpTransport,
}

type Config struct {
	// TODO: Remove when the rootfs path is constant.
	// Only used for v1 rootfs paths format.
	BaseTemplateID string

	Vcpu  int64
	RamMB int64

	// TotalDiskSizeMB optional, now used only for metrics.
	TotalDiskSizeMB   int64
	HugePages         bool
	FreePageReporting bool
	FreePageHinting   bool

	Envd EnvdMetadata

	FirecrackerConfig fc.Config

	// SkipEnvdWait skips the post-resume wait for envd readiness. Used by the
	// resume-build gdb debugging flow: the guest is held at a gdb entry
	// breakpoint and never boots envd, so the readiness wait would otherwise
	// time out and tear the sandbox down before a debugger can attach.
	SkipEnvdWait bool

	VolumeMounts []VolumeMountConfig

	MaxSandboxLengthHours int64

	// mu protects mutable sub-fields of Network (Egress, Ingress).
	// The Network pointer itself is set once at construction and never replaced.
	mu      *sync.RWMutex
	Network *orchestrator.SandboxNetworkConfig
}

// NewConfig creates a Config, normalizing a nil Network to an empty config
// so that Network is never nil.
func NewConfig(c Config) *Config {
	if c.Network == nil {
		c.Network = &orchestrator.SandboxNetworkConfig{}
	}

	c.mu = &sync.RWMutex{}

	return &c
}

// GetNetworkEgress returns the egress config in a thread-safe manner.
func (c *Config) GetNetworkEgress() *orchestrator.SandboxNetworkEgressConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.Network.GetEgress()
}

// SetNetworkEgress updates the egress config in a thread-safe manner.
func (c *Config) SetNetworkEgress(egress *orchestrator.SandboxNetworkEgressConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Network.Egress = egress
}

// GetNetworkIngress returns the ingress config in a thread-safe manner.
func (c *Config) GetNetworkIngress() *orchestrator.SandboxNetworkIngressConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.Network.GetIngress()
}

type VolumeMountConfig struct {
	ID   uuid.UUID
	Name string
	Path string
	Type string
}

type EnvdMetadata struct {
	Vars           map[string]string
	DefaultUser    *string
	DefaultWorkdir *string
	AccessToken    *string
	Version        string
}

// SandboxType distinguishes build sandboxes from regular sandboxes.
type SandboxType string

const (
	SandboxTypeSandbox SandboxType = "sandbox"
	SandboxTypeBuild   SandboxType = "build"
)

// String returns the sandbox type as a string, defaulting to "sandbox" if empty.
func (t SandboxType) String() string {
	if t == "" {
		return string(SandboxTypeSandbox)
	}

	return string(t)
}

type RuntimeMetadata struct {
	TemplateID  string
	SandboxID   string
	ExecutionID string

	// TeamID is best-effort metadata; not always populated so do not use for
	// decisions or feature-flag targeting.
	TeamID string

	BuildID     string
	SandboxType SandboxType
}

// sandboxLDContext builds an LD context with envd/kernel/FC-version attributes for
// per-sandbox flag targeting. Team/template targeting comes from the team and
// template contexts the caller embeds in ctx.
func sandboxLDContext(runtime RuntimeMetadata, config *Config) ldcontext.Context {
	return ldcontext.NewBuilder(runtime.SandboxID).
		Kind(featureflags.SandboxKind).
		SetString(featureflags.SandboxTemplateAttribute, runtime.TemplateID).
		SetString(featureflags.SandboxKernelVersionAttribute, config.FirecrackerConfig.KernelVersion).
		SetString(featureflags.SandboxFirecrackerVersionAttribute, config.FirecrackerConfig.FirecrackerVersion).
		SetString(featureflags.SandboxEnvdVersionAttribute, config.Envd.Version).
		SetString(featureflags.SandboxTypeAttribute, runtime.SandboxType.String()).
		Build()
}

type Resources struct {
	Slot   *network.Slot
	rootfs rootfs.Provider
	memory uffd.MemoryBackend
}

type internalConfig struct {
	EnvdInitRequestTimeout time.Duration

	// envdServerURLOverride, when non-empty, replaces the default
	// http://<slot-ip>:<envd-port> base address used for envd HTTP calls.
	// Test-only: it lets unit tests point envd ops (e.g. fsfreeze/fsthaw) at an
	// httptest server.
	envdServerURLOverride string
}

type Metadata struct {
	internalConfig internalConfig
	Config         *Config
	Runtime        RuntimeMetadata

	rwmu      sync.RWMutex // protects startedAt, endAt
	startedAt time.Time
	endAt     time.Time
}

// GetEndAt returns the sandbox end time in a thread-safe manner.
func (m *Metadata) GetEndAt() time.Time {
	m.rwmu.RLock()
	defer m.rwmu.RUnlock()

	return m.endAt
}

// SetEndAt sets the sandbox end time in a thread-safe manner.
func (m *Metadata) SetEndAt(t time.Time) {
	m.rwmu.Lock()
	defer m.rwmu.Unlock()

	m.endAt = t
}

type Sandbox struct {
	*Resources
	*Metadata

	// LifecycleID is a unique identifier for each Firecracker process.
	// It is used internally by the orchestrator for map eviction guards
	// and proxy connection pooling. Unlike ExecutionID (which is stable
	// across checkpoints and shared with the API), LifecycleID changes
	// every time a new Firecracker VM is started.
	LifecycleID string

	config  cfg.BuilderConfig
	files   *storage.SandboxFiles
	cleanup *Cleanup

	sandboxes *Map

	featureFlags *featureflags.Client

	process      *fc.Process
	cgroupHandle *cgroup.CgroupHandle

	Template template.Template

	Checks *Checks

	hostStatsCollector *HostStatsCollector

	// Deprecated: to be removed in the future
	// It was used to store the config to allow API restarts
	APIStoredConfig *orchestrator.SandboxConfig

	CABundle string

	exit *utils.ErrorOnce

	stop utils.Lazy[error]

	// startupStatsOnce guards the orchestrator.sandbox.uffd.startup.* recording
	// so it fires only on the first WaitForEnvd — the actual sandbox start.
	// ServeStats() is lifetime-cumulative on the UFFD handler, so a later
	// WaitForEnvd on the same handler (e.g. the envd-binary swap + restart in a
	// template build) would otherwise emit a sample inflated with post-startup
	// faults rather than that init's working set.
	startupStatsOnce sync.Once

	// skipStartupMetrics suppresses the per-start KPI histograms (envd-init
	// duration, uffd startup pages/source-pages/bytes) for a throwaway resume,
	// so the warm harvest never pollutes the customer resume distributions. Set
	// from the WithoutLiveRegistration resume option.
	skipStartupMetrics bool
}

func (s *Sandbox) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  s.Runtime.SandboxID,
		TemplateID: s.Runtime.TemplateID,
		TeamID:     s.Runtime.TeamID,
	}
}

// GetStartedAt returns the sandbox start time in a thread-safe manner.
func (m *Metadata) GetStartedAt() time.Time {
	m.rwmu.RLock()
	defer m.rwmu.RUnlock()

	return m.startedAt
}

// SetStartedAt sets the sandbox start time in a thread-safe manner.
func (m *Metadata) SetStartedAt(t time.Time) {
	m.rwmu.Lock()
	defer m.rwmu.Unlock()

	m.startedAt = t
}

type Factory struct {
	Sandboxes         *Map
	config            cfg.BuilderConfig
	networkPool       *network.Pool
	devicePool        *nbd.DevicePool
	featureFlags      *featureflags.Client
	hostStatsDelivery hoststats.Delivery
	cgroupManager     cgroup.Manager
	egressProxy       network.EgressProxy
}

func NewFactory(
	config cfg.BuilderConfig,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	featureFlags *featureflags.Client,
	hostStatsDelivery hoststats.Delivery,
	cgroupManager cgroup.Manager,
	egressProxy network.EgressProxy,
	sandboxes *Map,
) *Factory {
	return &Factory{
		Sandboxes:         sandboxes,
		config:            config,
		networkPool:       networkPool,
		devicePool:        devicePool,
		featureFlags:      featureFlags,
		hostStatsDelivery: hostStatsDelivery,
		cgroupManager:     cgroupManager,
		egressProxy:       egressProxy,
	}
}

func (f *Factory) EgressProxy() network.EgressProxy {
	return f.egressProxy
}

// PreBootFn is an optional callback invoked after the rootfs is ready but before
// Firecracker boots. It receives the rootfs device path (e.g., a file path for
// DirectProvider or /dev/nbdX for NBDProvider) and may modify the filesystem
// on the host side.
type PreBootFn func(ctx context.Context, rootfsPath string) error

type createOptions struct {
	deferMarkRunning bool
}

type CreateOption func(*createOptions)

// WithDeferredMarkRunning skips marking the sandbox running inside CreateSandbox
// so the caller can mark it only after envd is ready, matching ResumeSandbox.
// Used by the reboot path, where the guest is cold-booting and must not be
// routable until envd answers.
func WithDeferredMarkRunning() CreateOption {
	return func(o *createOptions) { o.deferMarkRunning = true }
}

// CreateSandbox creates the sandbox.
// IMPORTANT: You must Close() the sandbox after you are done with it.
func (f *Factory) CreateSandbox(
	ctx context.Context,
	config *Config,
	runtime RuntimeMetadata,
	template template.Template,
	sandboxTimeout time.Duration,
	rootfsCachePath string,
	processOptions fc.ProcessOptions,
	apiConfigToStore *orchestrator.SandboxConfig,
	preBootFn PreBootFn,
	opts ...CreateOption,
) (s *Sandbox, e error) {
	ctx, span := tracer.Start(ctx, "create sandbox")
	defer span.End()
	defer handleSpanError(span, &e)

	var createOpts createOptions
	for _, opt := range opts {
		opt(&createOpts)
	}

	execCtx, execSpan := startExecutionSpan(ctx)

	exit := utils.NewErrorOnce()

	cleanup := NewCleanup()
	defer func() {
		if e != nil {
			cleanupErr := cleanup.Run(ctx)
			e = errors.Join(e, cleanupErr)
			handleSpanError(execSpan, &e)
			execSpan.End()
		}
	}()

	lifecycleID := uuid.NewString()

	ipsPromise := getNetworkSlot(ctx, f.networkPool, cleanup, config.Network, f.Sandboxes.NetworkReleased)

	sandboxFiles := template.Files().NewSandboxFiles(runtime.SandboxID)
	cleanup.Add(ctx, cleanupFiles(f.config, sandboxFiles))

	rootFS, err := template.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs: %w", err)
	}

	var rootfsProvider rootfs.Provider
	if rootfsCachePath == "" {
		rootfsProvider, err = rootfs.NewNBDProvider(
			ctx,
			rootFS,
			sandboxFiles.SandboxCacheRootfsPath(f.config.StorageConfig),
			f.devicePool,
			f.featureFlags,
		)
	} else {
		rootfsProvider, err = rootfs.NewDirectProvider(
			ctx,
			rootFS,
			// Populate direct cache directly from the source file
			// This is needed for marking all blocks as dirty and being able to read them directly
			rootfsCachePath,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create rootfs overlay: %w", err)
	}
	cleanup.Add(ctx, rootfsProvider.Close)
	go func() {
		runErr := rootfsProvider.Start(execCtx)
		if runErr != nil {
			logger.L().Error(ctx, "rootfs overlay error", zap.Error(runErr))
		}
	}()

	memfile, err := template.Memfile(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get memfile: %w", err)
	}

	memfileSize, err := memfile.Size(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get memfile size: %w", err)
	}

	// / ==== END of resources initialization ====
	ips, err := ipsPromise.Wait(ctx)
	if err != nil {
		return nil, err
	}

	// Run the optional pre-boot hook before Firecracker starts.
	// This allows host-side filesystem changes before the guest kernel takes charge.
	if preBootFn != nil {
		rootfsPath, pathErr := rootfsProvider.Path()
		if pathErr != nil {
			return nil, fmt.Errorf("failed to get rootfs path for pre-boot hook: %w", pathErr)
		}

		if hookErr := preBootFn(ctx, rootfsPath); hookErr != nil {
			return nil, fmt.Errorf("pre-boot hook failed: %w", hookErr)
		}
	}

	cgroupHandle, cgroupFD := createCgroup(ctx, f.cgroupManager, sandboxFiles.SandboxCgroupName())
	defer releaseCgroupFD(ctx, cgroupHandle, runtime.SandboxID)

	cleanup.Add(ctx, func(ctx context.Context) error {
		return cgroupHandle.Remove(ctx)
	})

	fcHandle, err := fc.NewProcess(
		ctx,
		execCtx,
		f.config,
		ips,
		sandboxFiles,
		config.FirecrackerConfig,
		rootfsProvider,
		fc.ConstantRootfsPaths,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to init FC: %w", err)
	}

	throttleConfig := featureflags.GetTCPFirewallEgressThrottleConfig(ctx, f.featureFlags)
	driveThrottleConfig := featureflags.GetBlockDriveThrottleConfig(ctx, f.featureFlags)

	telemetry.ReportEvent(ctx, "created fc client")

	fcPageSize := int64(header.PageSize)
	if config.HugePages {
		fcPageSize = int64(header.HugepageSize)
	}
	resources := &Resources{
		Slot:   ips,
		rootfs: rootfsProvider,
		memory: uffd.NewNoopMemory(memfileSize, fcPageSize),
	}

	metadata := &Metadata{
		internalConfig: internalConfig{
			EnvdInitRequestTimeout: f.GetEnvdInitRequestTimeout(ctx),
		},

		Config:  config,
		Runtime: runtime,

		startedAt: time.Now(),
		endAt:     time.Now().Add(sandboxTimeout),
	}

	sbx := &Sandbox{
		LifecycleID: lifecycleID,

		Resources:    resources,
		Metadata:     metadata,
		cgroupHandle: cgroupHandle,

		Template:  template,
		config:    f.config,
		files:     sandboxFiles,
		process:   fcHandle,
		sandboxes: f.Sandboxes,

		cleanup:      cleanup,
		featureFlags: f.featureFlags,

		APIStoredConfig: apiConfigToStore,

		CABundle: f.egressProxy.CABundle(),

		exit: exit,
	}

	f.Sandboxes.AssignNetwork(ctx, sbx)
	cleanup.Add(ctx, func(ctx context.Context) error {
		f.Sandboxes.MarkStopping(ctx, runtime.SandboxID, sbx.LifecycleID)

		return nil
	})

	initializeHostStatsCollector(execCtx, sbx, runtime, config, f.hostStatsDelivery)

	// Collect a final stats sample on cleanup while the cgroup is still alive.
	cleanup.Add(ctx, func(ctx context.Context) error {
		if sbx.hostStatsCollector != nil {
			sbx.hostStatsCollector.Stop(ctx)
		}

		return nil
	})

	freePageHinting := fc.FCSupportsFreePageHinting(config.FirecrackerConfig.FirecrackerVersion) && config.FreePageHinting

	err = fcHandle.Create(
		ctx,
		sbxlogger.SandboxMetadata{
			SandboxID:  runtime.SandboxID,
			TemplateID: runtime.TemplateID,
			TeamID:     runtime.TeamID,
		},
		config.Vcpu,
		config.RamMB,
		config.HugePages,
		config.FreePageReporting,
		freePageHinting,
		processOptions,
		fc.RateLimiterConfig{
			Ops:       fc.TokenBucketConfig(throttleConfig.Ops),
			Bandwidth: fc.TokenBucketConfig(throttleConfig.Bandwidth),
		},
		fc.RateLimiterConfig{
			Ops:       fc.TokenBucketConfig(driveThrottleConfig.Ops),
			Bandwidth: fc.TokenBucketConfig(driveThrottleConfig.Bandwidth),
		},
		cgroupFD,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create FC: %w", err)
	}
	telemetry.ReportEvent(ctx, "created fc process")

	sbx.Checks = NewChecks(sbx)

	// Stop the sandbox first if it is still running, otherwise do nothing
	cleanup.AddPriority(ctx, sbx.Stop)

	go func() {
		defer execSpan.End()

		ctx, span := tracer.Start(execCtx, "sandbox-exit-wait")
		defer span.End()

		// If the process exists, stop the sandbox properly
		fcErr := fcHandle.Exit.Wait()
		err := sbx.Stop(ctx)

		exit.SetError(errors.Join(err, fcErr))
	}()

	if !createOpts.deferMarkRunning {
		f.Sandboxes.MarkRunning(ctx, sbx)
	}

	return sbx, nil
}

// Usage: defer handleSpanError(span, &err)
func handleSpanError(span trace.Span, err *error) {
	defer span.End()
	if err != nil && *err != nil {
		span.RecordError(*err)
		span.SetStatus(codes.Error, (*err).Error())
	}
}

// resumeOptions carries the optional knobs of ResumeSandbox.
type resumeOptions struct {
	// denyEgress isolates the resumed sandbox from the network (except the
	// orchestrator control path) before it is resumed.
	denyEgress bool
	// skipLiveRegistration keeps the resumed sandbox out of the live registry
	// (not addressable, not counted, no health checks) for throwaways the caller
	// reaps itself.
	skipLiveRegistration bool
}

// ResumeOption customizes a ResumeSandbox call.
type ResumeOption func(*resumeOptions)

// WithDenyEgress denies all network egress for the resumed sandbox — except the
// orchestrator control path — before Firecracker is resumed, so neither envd
// init nor any briefly unfrozen workload can reach the network. It is used for
// the throwaway pause-resume prefetch harvest sandbox, which is reaped as soon
// as its resume working set has been recorded.
func WithDenyEgress() ResumeOption {
	return func(o *resumeOptions) { o.denyEgress = true }
}

// WithoutLiveRegistration resumes the sandbox without adding it to the live
// registry and without starting health checks. The sandbox is not addressable
// via the sandbox map, is not counted in the node's reported allocation, and
// emits no per-sandbox metrics — for throwaways (e.g. the pause-resume prefetch
// harvest) that the caller reaps itself rather than promoting to a live
// sandbox. The network IP mapping is still assigned so the resume's own
// teardown stays symmetric.
func WithoutLiveRegistration() ResumeOption {
	return func(o *resumeOptions) { o.skipLiveRegistration = true }
}

// ThrowawayResumeOptions are the resume options for a caller-reaped throwaway
// (e.g. the pause-resume prefetch harvest): network-isolated and kept out of the
// live registry. It is the single source of truth for that option set so callers
// can't drift, and so the set can be asserted in one place.
func ThrowawayResumeOptions() []ResumeOption {
	return []ResumeOption{WithDenyEgress(), WithoutLiveRegistration()}
}

// ResumeSandbox resumes the sandbox from already saved template or snapshot.
// IMPORTANT: You must Close() the sandbox after you are done with it.
func (f *Factory) ResumeSandbox(
	ctx context.Context,
	t template.Template,
	config *Config,
	runtime RuntimeMetadata,
	startedAt time.Time,
	endAt time.Time,
	apiConfigToStore *orchestrator.SandboxConfig,
	opts ...ResumeOption,
) (s *Sandbox, e error) {
	ctx, span := tracer.Start(ctx, "resume sandbox")
	defer span.End()
	defer handleSpanError(span, &e)

	var ropts resumeOptions
	for _, opt := range opts {
		opt(&ropts)
	}

	execCtx, execSpan := startExecutionSpan(ctx)

	exit := utils.NewErrorOnce()

	cleanup := NewCleanup()
	defer func() {
		if e != nil {
			cleanupErr := cleanup.Run(ctx)
			e = errors.Join(e, cleanupErr)
			handleSpanError(execSpan, &e)
			execSpan.End()
		}
	}()

	lifecycleID := uuid.NewString()

	sandboxFiles := t.Files().NewSandboxFiles(runtime.SandboxID)
	cleanup.Add(ctx, cleanupFiles(f.config, sandboxFiles))

	telemetry.ReportEvent(ctx, "created sandbox files")

	// Uffd initialization
	fcUffdPath := sandboxFiles.SandboxUffdSocketPath()
	uffdPromise := utils.NewPromise(func() (*uffd.Uffd, error) {
		memfile, err := t.Memfile(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get memfile: %w", err)
		}

		telemetry.ReportEvent(ctx, "got template memfile")

		return uffd.New(memfile, fcUffdPath), nil
	})

	// Prefetching
	go func() {
		memfile, err := t.Memfile(ctx)
		if err != nil {
			return
		}

		meta, err := t.Metadata()
		if err != nil {
			return
		}

		telemetry.ReportEvent(ctx, "got metadata")

		// Start background prefetcher as early as possible if prefetch mapping exists
		// Fetching from source starts immediately; copying waits for uffd to be ready
		if meta.Prefetch != nil && meta.Prefetch.Memory != nil {
			fcUffd, err := uffdPromise.Wait(ctx)
			if err != nil {
				return
			}

			telemetry.ReportEvent(ctx, "starting prefetcher")
			l := logger.L().With(logger.WithSandboxID(runtime.SandboxID), logger.WithTemplateID(runtime.TemplateID), logger.WithTeamID(runtime.TeamID))

			go func() {
				p := prefetch.New(
					l,
					memfile,
					fcUffd,
					meta.Prefetch.Memory,
					f.featureFlags,
				)
				err := p.Start(execCtx)
				if err != nil {
					l.Error(ctx, "failed to start prefetcher", zap.Error(err))
				}
			}()
		}
	}()

	// Slot initialization
	ipsPromise := getNetworkSlot(ctx, f.networkPool, cleanup, config.Network, f.Sandboxes.NetworkReleased)

	// Rootfs initialization
	overlayPromise := utils.NewPromise(func() (rootfs.Provider, error) {
		readonlyRootfs, err := t.Rootfs()
		if err != nil {
			return nil, fmt.Errorf("failed to get rootfs: %w", err)
		}

		telemetry.ReportEvent(ctx, "got template rootfs")

		overlay, err := rootfs.NewNBDProvider(
			ctx,
			readonlyRootfs,
			sandboxFiles.SandboxCacheRootfsPath(f.config.StorageConfig),
			f.devicePool,
			f.featureFlags,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create rootfs overlay: %w", err)
		}

		cleanup.Add(ctx, overlay.Close)

		telemetry.ReportEvent(ctx, "created rootfs overlay")

		go func() {
			runErr := overlay.Start(execCtx)
			if runErr != nil {
				logger.L().Error(ctx, "rootfs overlay error", zap.Error(runErr))
			}
		}()

		return overlay, nil
	})

	// Memory initialization
	memoryPromise := utils.NewPromise(func() (struct{}, error) {
		fcUffd, err := uffdPromise.Wait(ctx)
		if err != nil {
			return struct{}{}, err
		}

		err = serveMemory(
			execCtx,
			cleanup,
			fcUffd,
			runtime.SandboxID,
		)
		if err != nil {
			return struct{}{}, fmt.Errorf("failed to serve memory: %w", err)
		}

		telemetry.ReportEvent(ctx, "started serving memory")

		return struct{}{}, nil
	})

	// Wait for all resources to be initialized
	ips, err := ipsPromise.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get network slot: %w", err)
	}

	telemetry.ReportEvent(ctx, "got network slot")

	// Isolate the sandbox from the network before it is resumed, so that neither
	// envd init nor any briefly unfrozen workload can egress while it runs. This
	// must happen before fcHandle.Resume below — denying on the returned handle
	// would be too late, as ResumeSandbox blocks until envd init has completed.
	if ropts.denyEgress {
		if err := ips.DenyEgress(ctx); err != nil {
			return nil, fmt.Errorf("failed to deny egress for resumed sandbox: %w", err)
		}

		telemetry.ReportEvent(ctx, "denied egress for resumed sandbox")
	}

	overlay, err := overlayPromise.Wait(ctx)
	if err != nil {
		return nil, err
	}

	_, err = memoryPromise.Wait(ctx)
	if err != nil {
		return nil, err
	}
	// ==== END of resources initialization ====

	rootfs, err := t.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs overlay: %w", err)
	}

	meta, err := t.Metadata()
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	// Create cgroup for sandbox resource accounting
	cgroupHandle, cgroupFD := createCgroup(ctx, f.cgroupManager, sandboxFiles.SandboxCgroupName())
	defer releaseCgroupFD(ctx, cgroupHandle, runtime.SandboxID)

	cleanup.Add(ctx, func(ctx context.Context) error {
		return cgroupHandle.Remove(ctx)
	})

	fcHandle, fcErr := fc.NewProcess(
		ctx,
		execCtx,
		f.config,
		ips,
		sandboxFiles,
		// The versions need to base exactly the same as the paused sandbox template because of the FC compatibility.
		config.FirecrackerConfig,
		overlay,
		fc.RootfsPaths{
			TemplateVersion: meta.Version,
			TemplateID:      config.BaseTemplateID,
			BuildID:         rootfs.Header().Metadata.BaseBuildId.String(),
		},
	)
	if fcErr != nil {
		return nil, fmt.Errorf("failed to create FC: %w", fcErr)
	}

	resumeThrottleConfig := featureflags.GetTCPFirewallEgressThrottleConfig(ctx, f.featureFlags)
	resumeDriveThrottleConfig := featureflags.GetBlockDriveThrottleConfig(ctx, f.featureFlags)

	telemetry.ReportEvent(ctx, "created FC process")

	// todo: check if kernel, firecracker, and envd versions exist
	snapfile, err := t.Snapfile()
	if err != nil {
		return nil, fmt.Errorf("failed to get snapfile: %w", err)
	}

	telemetry.ReportEvent(ctx, "got snapfile")

	fcUffd, err := uffdPromise.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get uffd: %w", err)
	}

	resources := &Resources{
		Slot:   ips,
		rootfs: overlay,
		memory: fcUffd,
	}

	metadata := &Metadata{
		internalConfig: internalConfig{
			EnvdInitRequestTimeout: f.GetEnvdInitRequestTimeout(ctx),
		},

		Config:  config,
		Runtime: runtime,

		startedAt: startedAt,
		endAt:     endAt,
	}

	sbx := &Sandbox{
		LifecycleID: lifecycleID,

		Resources:    resources,
		Metadata:     metadata,
		cgroupHandle: cgroupHandle,

		Template:  t,
		config:    f.config,
		files:     sandboxFiles,
		process:   fcHandle,
		sandboxes: f.Sandboxes,

		cleanup:      cleanup,
		featureFlags: f.featureFlags,

		APIStoredConfig: apiConfigToStore,
		CABundle:        f.egressProxy.CABundle(),

		exit: exit,

		// A throwaway resume keeps its warm, customer-indistinguishable start out
		// of the per-resume KPI histograms (see WaitForEnvd).
		skipStartupMetrics: ropts.skipLiveRegistration,
	}

	useMemfd := fc.FCSupportsMemfd(config.FirecrackerConfig.FirecrackerVersion) &&
		f.featureFlags.BoolFlag(ctx, featureflags.UseMemFdFlag, sandboxLDContext(runtime, config))

	// Part of the sandbox as we need to stop Checks before pausing the sandbox
	// This is to prevent race condition of reporting unhealthy sandbox
	sbx.Checks = NewChecks(sbx)

	cleanup.AddPriority(ctx, func(ctx context.Context) error {
		// Stop the sandbox first if it is still running, otherwise do nothing
		return sbx.Stop(ctx)
	})

	// Register the sandbox IP before Resume so it is findable by source address
	// during the resume (e.g. for TCP firewall lookups). On failure the deferred cleanup
	// will remove it.
	f.Sandboxes.AssignNetwork(ctx, sbx)
	cleanup.Add(ctx, func(ctx context.Context) error {
		f.Sandboxes.MarkStopping(ctx, runtime.SandboxID, sbx.LifecycleID)

		return nil
	})

	// A throwaway also skips host-stats collection, so it emits no per-sandbox
	// host stats under its (unregistered) identity — consistent with not being in
	// the live registry. The cleanup below is nil-safe when the collector is unset.
	if !ropts.skipLiveRegistration {
		initializeHostStatsCollector(execCtx, sbx, runtime, config, f.hostStatsDelivery)
	}

	// Collect a final stats sample on cleanup while the cgroup is still alive.
	cleanup.Add(ctx, func(ctx context.Context) error {
		if sbx.hostStatsCollector != nil {
			sbx.hostStatsCollector.Stop(ctx)
		}

		return nil
	})

	uffdStartCtx, cancelUffdStartCtx := context.WithCancelCause(ctx)
	defer cancelUffdStartCtx(errors.New("uffd finished starting"))
	go func() {
		uffdWaitErr := fcUffd.Exit().Wait()

		cancelUffdStartCtx(fmt.Errorf("uffd process exited: %w", errors.Join(uffdWaitErr, context.Cause(uffdStartCtx))))
	}()
	fcStartErr := fcHandle.Resume(
		uffdStartCtx,
		sbxlogger.SandboxMetadata{
			SandboxID:  runtime.SandboxID,
			TemplateID: runtime.TemplateID,
			TeamID:     runtime.TeamID,
		},
		fcUffdPath,
		snapfile,
		fcUffd.Ready(),
		config.Envd.AccessToken,
		cgroupFD,
		useMemfd,
		fc.RateLimiterConfig{
			Ops:       fc.TokenBucketConfig(resumeThrottleConfig.Ops),
			Bandwidth: fc.TokenBucketConfig(resumeThrottleConfig.Bandwidth),
		},
		fc.RateLimiterConfig{
			Ops:       fc.TokenBucketConfig(resumeDriveThrottleConfig.Ops),
			Bandwidth: fc.TokenBucketConfig(resumeDriveThrottleConfig.Bandwidth),
		},
	)

	if fcStartErr != nil {
		return nil, fmt.Errorf("failed to start FC: %w", fcStartErr)
	}

	telemetry.ReportEvent(ctx, "initialized FC")

	if config.SkipEnvdWait {
		// gdb debugging: the guest is frozen at the entry breakpoint and never
		// boots envd, so skip the readiness wait (it would time out and tear the
		// sandbox down). The caller drives the VM via the gdb stub instead.
		telemetry.ReportEvent(execCtx, "skipping envd wait (gdb mode)")
	} else {
		telemetry.ReportEvent(execCtx, "waiting for envd")

		err = sbx.WaitForEnvd(
			ctx,
			StartTypeResume,
			f.GetEnvdTimeout(ctx),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to wait for sandbox start: %w", err)
		}
	}

	// A throwaway (e.g. the pause-resume prefetch harvest) is never promoted to a
	// live sandbox: keep it out of the live registry so it is not addressable and
	// does not inflate the node's reported allocation or emit per-sandbox metrics,
	// and skip health checks it would never need.
	if !ropts.skipLiveRegistration {
		f.Sandboxes.MarkRunning(ctx, sbx)
	}

	telemetry.ReportEvent(execCtx, "envd initialized")

	if !ropts.skipLiveRegistration {
		go sbx.Checks.Start(execCtx)
	}

	go func() {
		defer execSpan.End()

		ctx, span := tracer.Start(execCtx, "sandbox-exit-wait")
		defer span.End()

		// Wait for either uffd or fc process to exit
		select {
		case <-fcUffd.Exit().Done():
		case <-fcHandle.Exit.Done():
		}

		err := sbx.Stop(ctx)

		uffdWaitErr := fcUffd.Exit().Wait()
		fcErr := fcHandle.Exit.Wait()
		exit.SetError(errors.Join(err, fcErr, uffdWaitErr))
	}()

	return sbx, nil
}

func startExecutionSpan(ctx context.Context) (context.Context, trace.Span) {
	parentSpan := trace.SpanFromContext(ctx)

	ctx = context.WithoutCancel(ctx)
	ctx, span := tracer.Start(ctx, "execute sandbox", //nolint:spancheck // this is still just a helper method
		trace.WithNewRoot(),
	)

	parentSpan.AddLink(trace.LinkFromContext(ctx))

	return ctx, span //nolint:spancheck // this is still just a helper method
}

func (s *Sandbox) Wait(ctx context.Context) error {
	return s.exit.WaitWithContext(ctx)
}

func (s *Sandbox) Close(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if s.sandboxes != nil {
		s.sandboxes.MarkStopped(context.WithoutCancel(ctx), s)
	}

	if err != nil {
		return fmt.Errorf("failed to cleanup sandbox: %w", err)
	}

	return nil
}

// Stop kills the sandbox. It is safe to call multiple times; only the first
// call will actually perform the stop operation.
func (s *Sandbox) Stop(ctx context.Context) error {
	return s.stop.GetOrInit(func() error {
		return s.doStop(ctx)
	})
}

// doStop performs the actual stop operation.
func (s *Sandbox) doStop(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "sandbox-close")
	defer span.End()

	var errs []error

	// Stop the health checks before stopping the sandbox
	s.Checks.Stop()

	fcStopErr := s.process.Stop(ctx)
	if fcStopErr != nil {
		errs = append(errs, fmt.Errorf("failed to stop FC: %w", fcStopErr))
	}

	cgroupKillErr := s.cgroupHandle.Kill(ctx)
	if cgroupKillErr != nil {
		errs = append(errs, fmt.Errorf("failed to kill sandbox cgroup: %w", cgroupKillErr))
	}

	// The process should exit before the rest of cleanup, but memory shutdown
	// must still run if the wait context is canceled so UFFD can exit.
	// FC's own exit error is reported via the exit waiters, not as a stop
	// failure, so only a canceled wait counts as an error here.
	select {
	case <-s.process.Exit.Done():
	case <-ctx.Done():
		errs = append(errs, fmt.Errorf("failed waiting for FC exit: %w", ctx.Err()))
	}

	uffdStopErr := s.Resources.memory.Stop()
	if uffdStopErr != nil {
		errs = append(errs, fmt.Errorf("failed to stop uffd: %w", uffdStopErr))
	}

	return errors.Join(errs...)
}

func (s *Sandbox) Shutdown(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "shutdown sandbox")
	defer span.End()

	// Stop the health check before pausing the VM
	s.Checks.Stop()

	if err := s.process.Pause(ctx); err != nil {
		return fmt.Errorf("failed to pause VM: %w", err)
	}

	// This is required because the FC API doesn't support passing /dev/null
	cachePaths, err := storage.Paths{
		BuildID: uuid.New().String(),
	}.Cache(s.config.StorageConfig)
	if err != nil {
		return fmt.Errorf("failed to create cache paths: %w", err)
	}
	defer cachePaths.Close()

	// The snapfile is required only because the FC API doesn't support passing /dev/null
	snapfile := template.NewLocalFileLink(cachePaths.CacheSnapfile())
	defer snapfile.Close()

	err = s.process.CreateSnapshot(ctx, snapfile.Path())
	if err != nil {
		return fmt.Errorf("error creating snapshot: %w", err)
	}

	// This should properly flush rootfs to the underlying device.
	err = s.Close(ctx)
	if err != nil {
		return fmt.Errorf("error stopping sandbox: %w", err)
	}

	return nil
}

type pauseOptions struct {
	filesystemSnapshot bool
}

type PauseOption func(*pauseOptions)

// WithFilesystemSnapshot makes the pause produce a filesystem-only snapshot:
// guest memory is not snapshotted, only the filesystem (rootfs) is persisted.
// Resuming such a snapshot reboots the guest instead of restoring memory state.
// The default (no option) is a full memory snapshot.
func WithFilesystemSnapshot() PauseOption {
	return func(o *pauseOptions) { o.filesystemSnapshot = true }
}

// Pause creates a snapshot of the sandbox.
//
// Currently the memory snapshotting works like this:
//  1. We pause FC VM
//  2. We call FC snapshot endpoint without specifying memfile path. With our custom FC,
//     this only creates the snapfile and drains and flushes the disk.
//  3. We call custom FC endpoint that returns memory addresses of the sandbox memory, that we will process after.
//  4. In case of NoopMemory (the sandbox was not a resume) we also call the custom FC endpoint,
//     that returns info about resident memory pages and about empty memory pages.
//  5. Base on the info from the custom FC endpoint or from Uffd we copy the pages directly from the FC process to a local cache.
//  6. We then can either close the sandbox or resume it.
//
// With WithFilesystemSnapshot(), steps 3-5 are skipped: a guest sync flushes
// the page cache to disk before pause, CreateSnapshot is still called for its
// disk drain+flush side effect (the snapfile is not uploaded), and the memfile
// diff is empty (NoDiff).
func (s *Sandbox) Pause(
	ctx context.Context,
	m metadata.Template,
	useCase SnapshotUseCase,
	opts ...PauseOption,
) (st *Snapshot, e error) {
	var pauseOpts pauseOptions
	for _, opt := range opts {
		opt(&pauseOpts)
	}

	ctx, span := tracer.Start(ctx, "sandbox-snapshot", trace.WithAttributes(
		attribute.Bool("fs-only-snapshot", pauseOpts.filesystemSnapshot),
	))
	defer span.End()

	cleanup := NewCleanup()
	defer func() {
		// Cleanup the snapshot if an error occurs
		if e != nil {
			err := cleanup.Run(ctx)
			e = errors.Join(e, err)
		}
	}()

	cachePaths, err := storage.Paths{BuildID: m.Template.BuildID}.Cache(s.config.StorageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache paths: %w", err)
	}
	cleanup.AddNoContext(ctx, cachePaths.Close)

	buildID, err := uuid.Parse(cachePaths.BuildID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse build id: %w", err)
	}

	// Stop the health check before pausing the VM
	s.Checks.Stop()

	// Best-effort pre-pause guest reclaim (fstrim, sync, drop_caches,
	// compact_memory) on the live VM via envd. Per-step caps are LD-flag-driven;
	// all default to 0 which disables the chain entirely. Non-fatal.
	s.bestEffortReclaim(ctx)
	// reclaim freezes user cgroups; if pause/snapshot fails the sandbox stays
	// live, so unfreeze on error to avoid a permanently frozen live VM.
	// Only runs via cleanup.Run on the error path; success leaves the frozen
	// state intact so it persists into the snapshot.
	cleanup.Add(ctx, func(ctx context.Context) error {
		s.bestEffortUnfreeze(ctx)

		return nil
	})

	if pauseOpts.filesystemSnapshot {
		// FC never flushes the guest page cache and no memory snapshot will
		// preserve it, so the rootfs must be quiesced before pause or it would
		// persist missing acknowledged writes. This is mandatory, unlike the
		// best-effort reclaim above.
		if err := s.guestPrepareFsForPause(ctx, cleanup); err != nil {
			return nil, err
		}

		// Memory prefetch refers to the memfile, which is not persisted.
		m.Prefetch = nil
	}

	// Record the snapshot kind in metadata so the resume path picks reboot vs
	// memory-resume from the snapshot's own metadata (see metadata.IsFilesystemOnly).
	// Set unconditionally so a memory pause of a previously-rebooted (fs-only)
	// sandbox correctly clears the flag. MarkFilesystemOnly also upgrades the
	// metadata version when needed so the flag survives deserialize for snapshots
	// taken from a V1 template.
	m = m.MarkFilesystemOnly(pauseOpts.filesystemSnapshot)

	// Drain free-page-hinting before pause so the snapshot doesn't capture
	// pages the guest already considers free. Timeout per use case; 0 disables.
	if t := featureflags.GetFreePageHintingTimeout(ctx, s.featureFlags, string(useCase), sandboxLDContext(s.Runtime, s.Config)); t > 0 {
		drainCtx, cancel := context.WithTimeout(ctx, t)
		if err := s.process.DrainBalloon(drainCtx); err != nil {
			telemetry.ReportError(ctx, "balloon hinting drain failed (continuing pause)", err)
		}
		cancel()
	}

	if err := s.process.Pause(ctx); err != nil {
		return nil, fmt.Errorf("failed to pause VM: %w", err)
	}

	// Best-effort flush before the rootfs export goroutine closes the FC API
	// socket. Non-blocking on the reader; trades precision for pause latency.
	_ = s.process.FlushMetrics(ctx)

	// Snapfile is not closed as it's returned and cached for later use (like resume)
	snapfile := template.NewLocalFileLink(cachePaths.CacheSnapfile())
	cleanup.AddNoContext(ctx, snapfile.Close)

	// CreateSnapshot also drains and flushes the virtio disk in our custom FC, so
	// it runs even for a filesystem-only pause (which needs the disk flush); the
	// resulting snapfile is just not uploaded in that case.
	err = s.process.CreateSnapshot(ctx, snapfile.Path())
	if err != nil {
		return nil, fmt.Errorf("error creating snapshot: %w", err)
	}

	// Gather data for postprocessing
	originalRootfs, err := s.Template.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get original rootfs: %w", err)
	}

	// Start POSTPROCESSING
	//
	// For a filesystem-only pause the memory snapshot is skipped entirely: the
	// memfile diff stays NoDiff with no header, and the memfile-derived fields
	// stay zero so the snapshot and scheduling metadata carry rootfs only.
	mem := MemorySnapshot{
		Diff:       build.Diff(&build.NoDiff{}),
		DiffHeader: NewResolvedDiffHeader(nil),
	}
	if !pauseOpts.filesystemSnapshot {
		mem, err = s.processMemorySnapshot(ctx, buildID)
		if err != nil {
			return nil, err
		}
	}
	// NoDiff.Close is a no-op, so registering it for the filesystem-only case is
	// harmless and keeps the cleanup ordering identical to the memory path.
	cleanup.AddNoContext(ctx, mem.Diff.Close)

	rootfsDiff, rootfsHeader, err := pauseProcessRootfs(
		ctx,
		buildID,
		originalRootfs.Header(),
		&RootfsDiffCreator{
			rootfs:    s.rootfs,
			closeHook: s.Close,
		},
		s.config.DefaultCacheDir,
	)
	if err != nil {
		return nil, fmt.Errorf("error while post processing: %w", err)
	}
	cleanup.AddNoContext(ctx, rootfsDiff.Close)

	rootfsDiffHeader := NewResolvedDiffHeader(rootfsHeader)
	// Derive scheduling metadata synchronously so Pause never blocks on the
	// async memfile-dedup header: the memfile chain comes from the resolved
	// parent header plus the new build, whose exact bytes aren't known yet, so
	// we pass the pre-dedup dirty size as an upper bound. It is block-granular
	// (dirty blocks * diff block size) and counts pages before dedup drops the
	// base-identical ones, so it over-estimates. The rootfs copy is synchronous
	// today, so its new header carries the exact rootfs chain and bytes; if it
	// ever becomes async, switch it to the parent plus a dirty proxy like memfile.
	// mem.header is nil for a filesystem-only pause → rootfs-only metadata.
	schedulingMetadata := scheduling.FromHeaders(buildID, mem.header, rootfsHeader, mem.newBytes)

	metadataFileLink := template.NewLocalFileLink(cachePaths.CacheMetadata())
	cleanup.AddNoContext(ctx, metadataFileLink.Close)

	err = m.ToFile(metadataFileLink.Path())
	if err != nil {
		return nil, err
	}

	return &Snapshot{
		Snapfile:           snapfile,
		Metafile:           metadataFileLink,
		MemorySnapshot:     mem,
		RootfsDiff:         rootfsDiff,
		RootfsDiffHeader:   rootfsDiffHeader,
		SchedulingMetadata: schedulingMetadata,
		FilesystemSnapshot: pauseOpts.filesystemSnapshot,
		RootfsBlockSize:    originalRootfs.Header().Metadata.BlockSize,

		BuildID: buildID,

		cleanup: cleanup,
	}, nil
}

// MemorySnapshot bundles the products of memory postprocessing during a Pause:
// the memfile diff, its (async-resolved) header, and the block size. It is
// embedded in Snapshot. For a filesystem-only pause it is zero-valued except for
// an empty NoDiff and a resolved-nil header (see Snapshot.FilesystemSnapshot).
type MemorySnapshot struct {
	Diff       build.Diff
	DiffHeader *DiffHeader
	// BlockSize is captured synchronously at Pause time because NewUpload's
	// compression validation needs it before the async dedup header resolves;
	// the dedup memfile path produces a page-granular Diff.BlockSize() that
	// doesn't match the chunker-read granularity on restore.
	BlockSize uint64

	// header (base memfile) and newBytes (pre-dedup dirty-byte upper bound) are
	// scheduling inputs consumed only at Pause time, so they stay unexported.
	header   *header.Header
	newBytes uint64
}

// processMemorySnapshot copies the dirty guest memory pages into a local diff
// and builds its header — steps 3-5 of Pause. Only called for a full memory
// snapshot; a filesystem-only pause skips it. The returned diff's Close must be
// registered for cleanup by the caller.
func (s *Sandbox) processMemorySnapshot(ctx context.Context, buildID uuid.UUID) (MemorySnapshot, error) {
	originalMemfile, err := s.Template.Memfile(ctx)
	if err != nil {
		return MemorySnapshot{}, fmt.Errorf("failed to get original memfile: %w", err)
	}
	memfileHeader := originalMemfile.Header()

	memfileDiffMetadata, err := s.Resources.memory.DiffMetadata(ctx, s.process)
	if err != nil {
		return MemorySnapshot{}, fmt.Errorf("failed to get memfile metadata: %w", err)
	}
	recordSnapshotDiff(ctx, "memfile", memfileDiffMetadata, memfileHeader)

	var dedupBase block.ReadonlyDevice
	var dedupBestEffort, dedupDirectIO bool
	var dedupBudget block.DedupBudget
	dedupCfg := s.featureFlags.JSONFlag(ctx, featureflags.MemfileDiffDedupFlag, sandboxLDContext(s.Runtime, s.Config)).AsValueMap()
	if dedupCfg.Get("enabled").BoolValue() {
		dedupBase = originalMemfile
		dedupBestEffort = dedupCfg.Get("bestEffort").BoolValue()
		dedupDirectIO = dedupCfg.Get("directIO").BoolValue()
		dedupBudget = block.DedupBudget{
			MaxFetchWindowsPerBlock:        dedupCfg.Get("maxFetchWindowsPerBlock").IntValue(),
			MaxPromotedParentPagesPerBlock: dedupCfg.Get("maxPromotedParentPagesPerBlock").IntValue(),
			MaxPagesPerPromotedFrame:       dedupCfg.Get("maxPagesPerPromotedFrame").IntValue(),
			BlockFaultPct:                  dedupCfg.Get("blockFaultPct").IntValue(),
			FetchRunWindowPages:            dedupCfg.Get("fetchRunWindowPages").IntValue(),
		}
	}

	memfileDiff, memfileDiffHeader, err := pauseProcessMemory(
		ctx,
		buildID,
		memfileHeader,
		memfileDiffMetadata,
		s.config.DefaultCacheDir,
		s.process,
		s.memory.Memfd(ctx),
		s.featureFlags.BoolFlag(ctx, featureflags.MemfdBackgroundCopyFlag, sandboxLDContext(s.Runtime, s.Config)),
		dedupBase,
		dedupBestEffort,
		dedupDirectIO,
		dedupBudget,
	)
	if err != nil {
		return MemorySnapshot{}, fmt.Errorf("error while post processing: %w", err)
	}

	return MemorySnapshot{
		Diff:       memfileDiff,
		DiffHeader: memfileDiffHeader,
		BlockSize:  memfileHeader.Metadata.BlockSize,
		header:     memfileHeader,
		newBytes:   memfileDiffMetadata.Dirty.GetCardinality() * uint64(memfileDiffMetadata.BlockSize),
	}, nil
}

// FlushAndReadBalloonMetrics triggers an FC metrics flush and returns the
// updated cumulative virtio-balloon counters. Used by the FPH bench.
func (s *Sandbox) FlushAndReadBalloonMetrics(ctx context.Context) (fc.BalloonMetricsSnapshot, error) {
	return s.process.FlushAndReadBalloonMetrics(ctx)
}

// MemoryPrefetchData returns the ordered page fault data for prefetch mapping.
func (s *Sandbox) MemoryPrefetchData(ctx context.Context) (block.PrefetchData, error) {
	prefetchData, err := s.Resources.memory.PrefetchData(ctx)
	if err != nil {
		return block.PrefetchData{}, fmt.Errorf("failed to get prefetch data: %w", err)
	}

	return prefetchData, nil
}

func pauseProcessMemory(
	ctx context.Context,
	buildID uuid.UUID,
	originalHeader *header.Header,
	diffMetadata *header.DiffMetadata,
	cacheDir string,
	fc *fc.Process,
	memfd *block.Memfd,
	bgCopy bool,
	originalMemfile block.ReadonlyDevice,
	dedupBestEffort bool,
	dedupDirectIO bool,
	dedupBudget block.DedupBudget,
) (d build.Diff, h *DiffHeader, e error) {
	ctx, span := tracer.Start(ctx, "process-memory")
	defer span.End()

	memfileDiffPath := build.GenerateDiffCachePath(cacheDir, buildID.String(), build.Memfile)
	metaOut := utils.NewSetOnce[*header.DiffMetadata]()
	// ExportMemory owns memfd and closes it on all paths.
	cache, err := fc.ExportMemory(
		ctx, diffMetadata.Dirty, memfileDiffPath, diffMetadata.BlockSize, memfd, bgCopy,
		originalMemfile, dedupBestEffort, dedupDirectIO, dedupBudget, diffMetadata.Empty, metaOut,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to export memory: %w", err)
	}

	diff, err := build.NewLocalDiffFromCache(
		build.GetDiffStoreKey(buildID.String(), build.Memfile),
		cache,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create local diff from cache: %w", errors.Join(err, cache.Close()))
	}

	// Build the diff header on a goroutine so Pause returns without waiting
	// on memfd-dedup compare. ExportMemory resolves metaOut sync for every
	// other path, so Wait there is non-blocking; the goroutine is harmless.
	headerOut := utils.NewSetOnce[*header.Header]()
	go func() {
		setHeader := func(h *header.Header, err error) {
			if setErr := headerOut.SetResult(h, err); setErr != nil {
				logger.L().Warn(ctx, "set memfile diff header", zap.Error(setErr))
			}
		}
		meta, err := metaOut.Wait()
		if err != nil {
			setHeader(nil, err)

			return
		}
		// post == nil signals "no dedup ran" to the metric so it records
		// kind="none" with zero savings.
		post := meta
		if originalMemfile == nil {
			post = nil
		}
		recordSnapshotDedup(ctx, "memfile", diffMetadata, post, dedupBestEffort)
		setHeader(meta.ToDiffHeader(ctx, originalHeader, buildID))
	}()

	return diff, headerOut, nil
}

func pauseProcessRootfs(
	ctx context.Context,
	buildId uuid.UUID,
	originalHeader *header.Header,
	diffCreator DiffCreator,
	cacheDir string,
) (d build.Diff, h *header.Header, e error) {
	ctx, span := tracer.Start(ctx, "process-rootfs")
	defer span.End()

	rootfsDiffFile, err := build.NewLocalDiffFile(cacheDir, buildId.String(), build.Rootfs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create rootfs diff: %w", err)
	}

	rootfsDiffMetadata, err := diffCreator.process(ctx, rootfsDiffFile.File)
	if err != nil {
		err = errors.Join(err, rootfsDiffFile.Close())

		return nil, nil, fmt.Errorf("error creating diff: %w", err)
	}
	telemetry.ReportEvent(ctx, "exported rootfs")
	recordSnapshotDiff(ctx, "rootfs", rootfsDiffMetadata, originalHeader)

	rootfsDiff, err := rootfsDiffFile.CloseToDiff(int64(originalHeader.Metadata.BlockSize))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert rootfs diff file to local diff: %w", err)
	}
	telemetry.ReportEvent(ctx, "converted rootfs diff file to local diff")

	rootfsHeader, err := rootfsDiffMetadata.ToDiffHeader(ctx, originalHeader, buildId)
	if err != nil {
		err = errors.Join(err, rootfsDiff.Close())

		return nil, nil, fmt.Errorf("failed to create rootfs header: %w", err)
	}

	return rootfsDiff, rootfsHeader, nil
}

// createCgroup creates a cgroup for sandbox resource accounting.
// The caller is responsible for registering cleanup to remove the cgroup.
//
// Returns the CgroupHandle and the cgroup directory FD to pass to the
// Firecracker process or (nil, cgroup.NoCgroupFD) on error.
func createCgroup(ctx context.Context, cgroupManager cgroup.Manager, cgroupName string) (*cgroup.CgroupHandle, int) {
	ctx, span := tracer.Start(ctx, "sandbox-create-cgroup", trace.WithAttributes(
		attribute.String("cgroup_name", cgroupName),
	))
	defer span.End()

	handle, err := cgroupManager.Create(ctx, cgroupName)
	if err != nil {
		logger.L().Warn(ctx, "failed to create cgroup, continuing without cgroup accounting",
			zap.String("cgroup_name", cgroupName),
			zap.Error(err))

		telemetry.ReportEvent(ctx, "cgroup creation failed, continuing without accounting")

		return nil, cgroup.NoCgroupFD
	}

	return handle, handle.GetFD()
}

func getNetworkSlot(
	ctx context.Context,
	networkPool *network.Pool,
	cleanup *Cleanup,
	networkConfig *orchestrator.SandboxNetworkConfig,
	networkReleased network.ReleaseNotify,
) *utils.Promise[*network.Slot] {
	return utils.NewPromise(func() (*network.Slot, error) {
		ctx, span := tracer.Start(ctx, "get network-slot")
		defer span.End()

		slot, err := networkPool.Get(ctx, networkConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to get network slot: %w", err)
		}

		cleanup.Add(ctx, func(ctx context.Context) error {
			ctx, span := tracer.Start(ctx, "clean network-slot")
			defer span.End()

			// Async so sandbox cleanup doesn't block on the return delay or
			// network teardown; the pool's Close waits for in-flight returns.
			return networkPool.ReturnAsync(ctx, slot, networkReleased, network.ReturnDelay)
		})

		return slot, nil
	})
}

func serveMemory(
	ctx context.Context,
	cleanup *Cleanup,
	fcUffd *uffd.Uffd,
	sandboxID string,
) error {
	ctx, span := tracer.Start(ctx, "serve-memory")
	defer span.End()

	telemetry.ReportEvent(ctx, "created uffd")

	if err := fcUffd.Start(ctx, sandboxID); err != nil {
		return fmt.Errorf("failed to start uffd: %w", err)
	}

	telemetry.ReportEvent(ctx, "started uffd")

	cleanup.Add(ctx, func(ctx context.Context) error {
		_, span := tracer.Start(ctx, "uffd-stop")
		defer span.End()

		if err := fcUffd.Stop(); err != nil {
			return fmt.Errorf("failed to stop uffd: %w", err)
		}

		return nil
	})

	return nil
}

func (s *Sandbox) WaitForExit(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "sandbox-wait-for-exit")
	defer span.End()

	timeout := time.Until(s.GetEndAt())

	select {
	case <-time.After(timeout):
		return errors.New("waiting for exit took too long")
	case <-ctx.Done():
		return nil
	case <-s.exit.Done():
		err := s.exit.Error()
		if err == nil {
			return nil
		}

		return fmt.Errorf("fc process exited prematurely: %w", err)
	}
}

func (s *Sandbox) WaitForEnvd(
	ctx context.Context,
	startType StartType,
	timeout time.Duration,
) (e error) {
	start := time.Now()
	ctx, span := tracer.Start(ctx, "sandbox-wait-for-start")
	defer span.End()

	defer func() {
		// A throwaway (the pause-resume prefetch harvest) is warm by construction
		// and must not pollute the customer resume KPIs (envd-init duration,
		// startup pages/source-pages — the consume-side payoff signals) or even be
		// distinguishable in them, so it records none of these. It is otherwise
		// kept out of Prometheus (registration-skip); the harvest's own metrics
		// cover its timing/size.
		if !s.skipStartupMetrics {
			duration := time.Since(start).Milliseconds()
			// success is kept for backward compatibility until consumers move to exit_type.
			waitForEnvdDurationHistogram.Record(ctx, duration, metric.WithAttributes(
				telemetry.WithEnvdVersion(s.Config.Envd.Version),
				attribute.Int64("timeout_ms", s.internalConfig.EnvdInitRequestTimeout.Milliseconds()),
				attribute.Bool("success", e == nil),
				attribute.String("start_type", string(startType)),
				attribute.String("exit_type", string(classifyEnvdInitExit(e))),
			))

			// Record the demand-fault working set the guest needed to reach this
			// point. Only on the first WaitForEnvd: it is the actual start, and
			// ServeStats() is cumulative since resume, so at this instant it equals
			// the startup counts. A later WaitForEnvd on the same handler (e.g. the
			// envd-binary swap + restart during a template build) would otherwise
			// re-report a cumulative total polluted with intervening faults.
			// Recorded for both outcomes (success label) so slow/failed starts can
			// be correlated with page volume.
			s.startupStatsOnce.Do(func() {
				stats := s.memory.ServeStats()
				startupAttrs := metric.WithAttributes(
					attribute.String("start_type", string(startType)),
					attribute.Bool("success", e == nil),
				)
				uffdStartupPagesHistogram.Record(ctx, stats.Pages, startupAttrs)
				uffdStartupSourcePagesHistogram.Record(ctx, stats.SourcePages, startupAttrs)
				uffdStartupBytesHistogram.Record(ctx, stats.Bytes, startupAttrs)
			})
		}

		if e != nil {
			return
		}

		// Update the sandbox as started now
		s.SetStartedAt(time.Now())
	}()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	go func() {
		select {
		// Ensure the syncing takes at most timeout seconds.
		case <-time.After(timeout):
			cancel(ErrWaitForEnvdTimeout)
		case <-ctx.Done():
			return
		case <-s.process.Exit.Done():
			err := s.process.Exit.Error()

			cancel(fmt.Errorf("%w: %w", ErrFcProcessExited, err))
		}
	}()

	if err := s.initEnvd(ctx, startType); err != nil {
		return fmt.Errorf("failed to init new envd: %w", err)
	}

	telemetry.ReportEvent(ctx, fmt.Sprintf("[sandbox %s]: initialized new envd", s.Metadata.Runtime.SandboxID))

	return nil
}

func releaseCgroupFD(ctx context.Context, cgroupHandle *cgroup.CgroupHandle, sandboxID string) {
	if releaseErr := cgroupHandle.ReleaseCgroupFD(); releaseErr != nil {
		logger.L().Warn(ctx, "failed to release cgroup directory FD",
			logger.WithSandboxID(sandboxID),
			zap.Error(releaseErr))
	}
}

func (f *Factory) GetEnvdInitRequestTimeout(ctx context.Context) time.Duration {
	envdInitRequestTimeoutMs := f.featureFlags.IntFlag(ctx, featureflags.EnvdInitTimeoutMilliseconds)

	return time.Duration(envdInitRequestTimeoutMs) * time.Millisecond
}

func (f *Factory) GetEnvdTimeout(ctx context.Context) time.Duration {
	envdTimeoutMs := f.featureFlags.IntFlag(ctx, featureflags.EnvdTimeoutMilliseconds)

	return time.Duration(envdTimeoutMs) * time.Millisecond
}
