package v2

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	orchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// buildIDPattern validates that a build ID contains only safe characters.
// This prevents path traversal and shell metacharacter injection in rsync commands.
var buildIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// MigrationPreconditions defines compatibility requirements that must match
// between source and target hosts for a migration to be safe (§7.2).
// Restoring a snapshot on an incompatible host can silently corrupt the VM.
type MigrationPreconditions struct {
	Provider           string // "gcp", "aws", "azure"
	Region             string // "us-central1", "eu-west-1", etc.
	Architecture       string // "x86_64", "aarch64"
	CPUFamily          string // CPU model/stepping group
	HostKernelVersion  string // host kernel line (e.g., "6.1.x")
	FirecrackerVersion string // Firecracker binary version
	GuestImageABI      string // guest image ABI identifier
}

// Compatible checks whether two precondition sets allow migration between them.
func (p MigrationPreconditions) Compatible(target MigrationPreconditions) error {
	var errs []error
	if p.Architecture != "" && target.Architecture != "" && p.Architecture != target.Architecture {
		errs = append(errs, fmt.Errorf("architecture mismatch: source=%s target=%s", p.Architecture, target.Architecture))
	}
	if p.FirecrackerVersion != "" && target.FirecrackerVersion != "" && p.FirecrackerVersion != target.FirecrackerVersion {
		errs = append(errs, fmt.Errorf("firecracker version mismatch: source=%s target=%s", p.FirecrackerVersion, target.FirecrackerVersion))
	}
	if p.HostKernelVersion != "" && target.HostKernelVersion != "" && p.HostKernelVersion != target.HostKernelVersion {
		errs = append(errs, fmt.Errorf("host kernel mismatch: source=%s target=%s", p.HostKernelVersion, target.HostKernelVersion))
	}
	if p.GuestImageABI != "" && target.GuestImageABI != "" && p.GuestImageABI != target.GuestImageABI {
		errs = append(errs, fmt.Errorf("guest image ABI mismatch: source=%s target=%s", p.GuestImageABI, target.GuestImageABI))
	}
	if p.CPUFamily != "" && target.CPUFamily != "" && p.CPUFamily != target.CPUFamily {
		errs = append(errs, fmt.Errorf("CPU family mismatch: source=%s target=%s", p.CPUFamily, target.CPUFamily))
	}
	if len(errs) > 0 {
		return fmt.Errorf("migration precondition check failed: %w", errors.Join(errs...))
	}
	return nil
}

// MigrationDomain describes a migration/snapshot restore domain for a sandbox.
type MigrationDomain struct {
	ID         string
	SourceNode string
	TargetNode string
	SandboxID  string
	BuildID    string // snapshot build ID
	HostIP     net.IP // original host IP (preserved via forwarding)
	SlotIdx    int    // target slot index
	State      MigrationState

	// Preconditions for migration safety (§7.2).
	Preconditions MigrationPreconditions

	// Timing
	StartedAt   time.Time
	PausedAt    time.Time // when source paused
	TransferAt  time.Time // when file transfer completed
	ResumedAt   time.Time // when target resumed
	CompletedAt time.Time

	// Network forwarding
	OldHostIP    net.IP // source host IP being forwarded
	NewHostIP    net.IP // target host IP (new slot)
	ForwardViaWg bool   // whether WireGuard forwarding is active
}

// MigrationState represents the current phase of a migration.
type MigrationState string

const (
	MigrationStatePending    MigrationState = "pending"
	MigrationStateActive     MigrationState = "active"
	MigrationStateTransfer   MigrationState = "transfer"
	MigrationStateResuming   MigrationState = "resuming"
	MigrationStateForwarding MigrationState = "forwarding"
	MigrationStateCompleted  MigrationState = "completed"
	MigrationStateFailed     MigrationState = "failed"
)

// DefaultMigrationDomain returns a no-op domain (no migration in progress).
func DefaultMigrationDomain() *MigrationDomain {
	return &MigrationDomain{
		State: MigrationStateCompleted,
	}
}

// MigrationRequest describes a cross-node migration.
type MigrationRequest struct {
	SandboxID  string // sandbox to migrate
	TemplateID string // template ID for the sandbox
	BuildID    string // build ID for the snapshot

	SourceAddr string // source orchestrator gRPC address (host:port)
	TargetAddr string // target orchestrator gRPC address (host:port)

	SourceWgIP net.IP // source node's WireGuard IP (e.g., 10.99.0.2)
	TargetWgIP net.IP // target node's WireGuard IP (e.g., 10.99.0.1)
	WgDevice   string // WireGuard interface name (e.g., "wg0")

	// Snapshot transfer paths
	SourceCacheDir        string // source template cache dir (e.g., /tmp/template-cache)
	TargetCacheDir        string // target template cache dir
	SourceDefaultCacheDir string // source default cache dir (for diff files)
	TargetDefaultCacheDir string // target default cache dir

	// SandboxConfig for resume on target. If nil, the config is captured from
	// the running sandbox on source via List before pausing.
	TargetSandboxConfig *orchestrator.SandboxConfig

	// Preconditions for source and target. If both are set, they are checked
	// for compatibility before migration begins. If either is zero-value,
	// the check is skipped (PoC mode).
	SourcePreconditions MigrationPreconditions
	TargetPreconditions MigrationPreconditions

	// PreMigrateHook is called after config capture but before pause.
	// Use it to mark the sandbox as MIGRATING and drain connections.
	// If it returns an error, migration is aborted before any state change.
	PreMigrateHook func(ctx context.Context, sandboxID string) error

	// Timeout for the entire migration.
	Timeout time.Duration
}

// MigrationResult captures the outcome.
type MigrationResult struct {
	Domain       *MigrationDomain
	OldHostIP    net.IP // source node host IP (the one being forwarded)
	NewHostIP    net.IP // target node host IP (new slot)
	NewSandboxID string // sandbox ID on target

	// Timing breakdown
	PauseDuration    time.Duration
	TransferDuration time.Duration
	ResumeDuration   time.Duration
	TotalDuration    time.Duration
	DowntimeWindow   time.Duration // pause → resume complete
}

// MigrationCoordinator orchestrates cross-node sandbox migration.
// It connects to both source and target orchestrator gRPC services
// and executes the migration flow: pause → transfer → resume → forward.
type MigrationCoordinator struct {
	sourceConn   *grpc.ClientConn
	targetConn   *grpc.ClientConn
	sourceClient orchestrator.SandboxServiceClient
	targetClient orchestrator.SandboxServiceClient
}

// NewMigrationCoordinator creates a coordinator connected to both nodes.
//
// TODO: grpc.DialContext is deprecated since grpc-go v1.63. Migrate to
// grpc.NewClient when the rest of the codebase does.
func NewMigrationCoordinator(ctx context.Context, sourceAddr, targetAddr string, opts ...grpc.DialOption) (*MigrationCoordinator, error) {
	sourceConn, err := grpc.DialContext(ctx, sourceAddr, opts...)
	if err != nil {
		return nil, fmt.Errorf("dial source %s: %w", sourceAddr, err)
	}

	targetConn, err := grpc.DialContext(ctx, targetAddr, opts...)
	if err != nil {
		sourceConn.Close()
		return nil, fmt.Errorf("dial target %s: %w", targetAddr, err)
	}

	return &MigrationCoordinator{
		sourceConn:   sourceConn,
		targetConn:   targetConn,
		sourceClient: orchestrator.NewSandboxServiceClient(sourceConn),
		targetClient: orchestrator.NewSandboxServiceClient(targetConn),
	}, nil
}

// NewMigrationCoordinatorFromClients creates a coordinator from existing gRPC clients.
// Useful for testing or when connections are already established.
func NewMigrationCoordinatorFromClients(source, target orchestrator.SandboxServiceClient) *MigrationCoordinator {
	return &MigrationCoordinator{
		sourceClient: source,
		targetClient: target,
	}
}

// Close releases gRPC connections.
func (mc *MigrationCoordinator) Close() error {
	var errs []error
	if mc.sourceConn != nil {
		if err := mc.sourceConn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if mc.targetConn != nil {
		if err := mc.targetConn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close connections: %w", errors.Join(errs...))
	}
	return nil
}

// Migrate executes the full cross-node migration flow.
//
// Steps:
//  1. Validate inputs and preconditions
//  2. Capture sandbox config from source (if not provided)
//  3. Check storage mobility (reject host-local volumes)
//  4. Run PreMigrateHook (drain connections, mark MIGRATING)
//  5. Pause sandbox on source (creates snapshot)
//  6. Transfer snapshot files via rsync over WireGuard
//  7. Resume sandbox on target from snapshot (new execution_id)
//  8. Return result with timing (IP forwarding is set up separately)
//
// If any step fails after pause, the sandbox remains paused on source.
// The caller can resume it there as a fallback.
func (mc *MigrationCoordinator) Migrate(ctx context.Context, req MigrationRequest) (*MigrationResult, error) {
	// Step 1: Validate inputs and preconditions.
	if err := validateBuildID(req.BuildID); err != nil {
		return nil, err
	}

	if err := req.SourcePreconditions.Compatible(req.TargetPreconditions); err != nil {
		return nil, fmt.Errorf("migration preconditions: %w", err)
	}

	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	domain := &MigrationDomain{
		ID:            fmt.Sprintf("mig-%s-%d", req.SandboxID, time.Now().UnixMilli()),
		SourceNode:    req.SourceAddr,
		TargetNode:    req.TargetAddr,
		SandboxID:     req.SandboxID,
		BuildID:       req.BuildID,
		State:         MigrationStatePending,
		StartedAt:     time.Now(),
		Preconditions: req.SourcePreconditions,
	}

	result := &MigrationResult{Domain: domain}
	migrationStart := time.Now()

	// Step 2: Capture sandbox config from source before pausing.
	// Always clone so we never mutate the caller's proto.
	var cfg *orchestrator.SandboxConfig
	if req.TargetSandboxConfig != nil {
		cfg = proto.Clone(req.TargetSandboxConfig).(*orchestrator.SandboxConfig)
	} else {
		logger.L().Info(ctx, "migration: fetching sandbox config from source",
			zap.String("sandboxID", req.SandboxID),
		)
		captured, err := mc.captureSandboxConfig(ctx, req.SandboxID)
		if err != nil {
			domain.State = MigrationStateFailed
			return result, fmt.Errorf("capture sandbox config: %w", err)
		}
		cfg = captured
	}

	// Step 3: Check storage mobility — reject sandboxes with volume mounts
	// that can't be reattached on the target (§5.4, §7.5).
	if len(cfg.GetVolumeMounts()) > 0 {
		domain.State = MigrationStateFailed
		return result, fmt.Errorf(
			"sandbox has %d volume mount(s); cross-node migration requires shared or reattachable storage",
			len(cfg.GetVolumeMounts()),
		)
	}

	// Override snapshot-specific fields for the resume.
	cfg.Snapshot = true
	cfg.BuildId = req.BuildID
	cfg.BaseTemplateId = cfg.GetTemplateId()
	// Issue a new execution_id so stale proxy/edge routes on the old host
	// don't accidentally route to a dead endpoint (§11.5).
	cfg.ExecutionId = uuid.New().String()

	// Step 4: Run PreMigrateHook — mark sandbox as MIGRATING and drain.
	if req.PreMigrateHook != nil {
		logger.L().Info(ctx, "migration: running pre-migrate hook",
			zap.String("sandboxID", req.SandboxID),
		)
		if err := req.PreMigrateHook(ctx, req.SandboxID); err != nil {
			domain.State = MigrationStateFailed
			return result, fmt.Errorf("pre-migrate hook: %w", err)
		}
	}

	// Step 5: Pause sandbox on source.
	domain.State = MigrationStateActive
	logger.L().Info(ctx, "migration: pausing sandbox on source",
		zap.String("sandboxID", req.SandboxID),
		zap.String("source", req.SourceAddr),
	)

	pauseStart := time.Now()
	_, err := mc.sourceClient.Pause(ctx, &orchestrator.SandboxPauseRequest{
		SandboxId:  req.SandboxID,
		TemplateId: req.TemplateID,
		BuildId:    req.BuildID,
	})
	if err != nil {
		domain.State = MigrationStateFailed
		return result, fmt.Errorf("pause sandbox on source: %w", err)
	}
	domain.PausedAt = time.Now()
	result.PauseDuration = time.Since(pauseStart)

	logger.L().Info(ctx, "migration: sandbox paused",
		zap.Duration("duration", result.PauseDuration),
	)

	// Step 6: Transfer snapshot files via rsync over WireGuard.
	domain.State = MigrationStateTransfer
	transferStart := time.Now()

	if err := mc.transferSnapshot(ctx, req); err != nil {
		domain.State = MigrationStateFailed
		return result, fmt.Errorf("transfer snapshot: %w", err)
	}
	domain.TransferAt = time.Now()
	result.TransferDuration = time.Since(transferStart)

	logger.L().Info(ctx, "migration: snapshot transferred",
		zap.Duration("duration", result.TransferDuration),
	)

	// Step 7: Resume sandbox on target with new execution_id.
	domain.State = MigrationStateResuming
	resumeStart := time.Now()

	now := time.Now()
	_, err = mc.targetClient.Create(ctx, &orchestrator.SandboxCreateRequest{
		Sandbox:   cfg,
		StartTime: timestamppb.New(now),
		EndTime:   timestamppb.New(now.Add(1 * time.Hour)),
	})
	if err != nil {
		domain.State = MigrationStateFailed
		return result, fmt.Errorf("resume sandbox on target: %w", err)
	}
	domain.ResumedAt = time.Now()
	result.ResumeDuration = time.Since(resumeStart)
	// Note: Create response's ClientId is the orchestrator node ID, not the sandbox ID.
	result.NewSandboxID = cfg.GetSandboxId()

	logger.L().Info(ctx, "migration: sandbox resumed on target",
		zap.Duration("duration", result.ResumeDuration),
		zap.String("newSandboxID", result.NewSandboxID),
		zap.String("newExecutionID", cfg.GetExecutionId()),
	)

	// Step 8: Mark complete. IP forwarding is set up separately via SetupForwarding.
	domain.State = MigrationStateForwarding
	domain.CompletedAt = time.Now()
	domain.State = MigrationStateCompleted

	result.TotalDuration = time.Since(migrationStart)
	result.DowntimeWindow = domain.ResumedAt.Sub(domain.PausedAt)

	logger.L().Info(ctx, "migration: completed",
		zap.Duration("total", result.TotalDuration),
		zap.Duration("downtime", result.DowntimeWindow),
		zap.Duration("pause", result.PauseDuration),
		zap.Duration("transfer", result.TransferDuration),
		zap.Duration("resume", result.ResumeDuration),
	)

	return result, nil
}

// SetupForwarding sets up IP forwarding after migration.
// Call this on the source node to forward old host IP through WireGuard,
// and on the target node to DNAT to the new slot.
//
// SECURITY NOTE: WireGuard-forwarded traffic bypasses the TCP firewall proxy
// because it arrives on wg0 (not a v2 veth). This is acceptable for the PoC
// because IP forwarding is a stopgap — production uses edge/egress service
// cutover which routes through the normal proxy path (§7.3 step 11).
//
// targetHF is the host firewall on the target node (can be nil if running on source only).
func (mc *MigrationCoordinator) SetupForwarding(ctx context.Context, req MigrationRequest, domain *MigrationDomain, targetHF *HostFirewall) error {
	if domain.OldHostIP == nil || domain.NewHostIP == nil {
		return fmt.Errorf("forwarding IPs not set on domain (old=%v, new=%v)", domain.OldHostIP, domain.NewHostIP)
	}

	// Source side: route old IP through WireGuard.
	logger.L().Info(ctx, "migration: setting up IP forward on source",
		zap.String("oldIP", domain.OldHostIP.String()),
		zap.String("targetWgIP", req.TargetWgIP.String()),
		zap.String("wgDevice", req.WgDevice),
	)

	if err := SetupIPForward(domain.OldHostIP, req.TargetWgIP, req.WgDevice); err != nil {
		return fmt.Errorf("setup IP forward: %w", err)
	}

	// Target side: DNAT old IP to new slot IP.
	if targetHF != nil {
		logger.L().Info(ctx, "migration: setting up DNAT on target",
			zap.String("oldIP", domain.OldHostIP.String()),
			zap.String("newIP", domain.NewHostIP.String()),
		)

		if err := SetupMigrationDNAT(targetHF, domain.OldHostIP, domain.NewHostIP, req.WgDevice); err != nil {
			// Best-effort: try to undo the route.
			_ = TeardownIPForward(domain.OldHostIP, req.WgDevice)
			return fmt.Errorf("setup migration DNAT: %w", err)
		}
	}

	domain.ForwardViaWg = true

	return nil
}

// TeardownForwarding removes IP forwarding for a migrated sandbox.
func (mc *MigrationCoordinator) TeardownForwarding(ctx context.Context, domain *MigrationDomain, wgDevice string, targetHF *HostFirewall) error {
	if !domain.ForwardViaWg {
		return nil
	}

	var errs []error

	// Source side: remove route.
	if err := TeardownIPForward(domain.OldHostIP, wgDevice); err != nil {
		logger.L().Warn(ctx, "migration: failed to teardown IP forward",
			zap.Error(err),
		)
		errs = append(errs, err)
	}

	// Target side: remove DNAT.
	if targetHF != nil {
		if err := TeardownMigrationDNAT(targetHF, domain.OldHostIP); err != nil {
			logger.L().Warn(ctx, "migration: failed to teardown DNAT",
				zap.Error(err),
			)
			errs = append(errs, err)
		}
	}

	domain.ForwardViaWg = false

	if len(errs) > 0 {
		return fmt.Errorf("teardown forwarding: %w", errors.Join(errs...))
	}
	return nil
}

// captureSandboxConfig fetches the running sandbox's full config from the source node.
// This ensures the resume on target uses the same VM parameters (Vcpu, RAM, kernel, etc.).
func (mc *MigrationCoordinator) captureSandboxConfig(ctx context.Context, sandboxID string) (*orchestrator.SandboxConfig, error) {
	listResp, err := mc.sourceClient.List(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("list sandboxes on source: %w", err)
	}

	for _, sb := range listResp.GetSandboxes() {
		if sb.GetConfig().GetSandboxId() == sandboxID {
			// Deep-copy so we don't mutate the gRPC response.
			return proto.Clone(sb.GetConfig()).(*orchestrator.SandboxConfig), nil
		}
	}

	return nil, fmt.Errorf("sandbox %s not found on source node", sandboxID)
}

// transferSnapshot uses rsync over WireGuard to copy snapshot files from source to target.
func (mc *MigrationCoordinator) transferSnapshot(ctx context.Context, req MigrationRequest) error {
	if err := validateBuildID(req.BuildID); err != nil {
		return err
	}

	targetWgIP := req.TargetWgIP.String()

	// Transfer template cache (snapfile + metadata).
	cacheSrc := fmt.Sprintf("%s/%s/", req.SourceCacheDir, req.BuildID)
	cacheDst := fmt.Sprintf("root@%s:%s/%s/", targetWgIP, req.TargetCacheDir, req.BuildID)

	logger.L().Info(ctx, "migration: transferring template cache",
		zap.String("src", cacheSrc),
		zap.String("dst", cacheDst),
	)

	cmd := exec.CommandContext(ctx, "rsync", "-az", "--mkpath",
		"-e", "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
		cacheSrc, cacheDst,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rsync template cache: %w\noutput: %s", err, string(out))
	}

	// Transfer diff files (memfile + rootfs deltas).
	diffSrc := fmt.Sprintf("%s/", req.SourceDefaultCacheDir)
	diffDst := fmt.Sprintf("root@%s:%s/", targetWgIP, req.TargetDefaultCacheDir)

	logger.L().Info(ctx, "migration: transferring diff files",
		zap.String("src", diffSrc),
		zap.String("dst", diffDst),
	)

	cmd = exec.CommandContext(ctx, "rsync", "-az", "--mkpath",
		"-e", "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
		"--include", fmt.Sprintf("%s*", req.BuildID),
		"--exclude", "*",
		diffSrc, diffDst,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rsync diff files: %w\noutput: %s", err, string(out))
	}

	return nil
}

// validateBuildID checks that the build ID is safe for use in file paths and
// shell arguments. Rejects path traversal sequences and special characters.
func validateBuildID(buildID string) error {
	if buildID == "" {
		return fmt.Errorf("build ID must not be empty")
	}
	if !buildIDPattern.MatchString(buildID) {
		return fmt.Errorf("build ID %q contains invalid characters (allowed: alphanumeric, dash, underscore, dot)", buildID)
	}
	return nil
}
