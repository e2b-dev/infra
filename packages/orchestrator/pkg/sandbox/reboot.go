//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	buildenvd "github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/constants"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/units"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	// minEnvdVersionForKVMClock is the minimum envd version that supports kvm-clock.
	minEnvdVersionForKVMClock = "0.2.11"

	// rebootEnvdTimeout bounds the systemd boot + envd start; a cold boot needs a
	// longer window than a memory resume (matches the template build's wait).
	rebootEnvdTimeout = 60 * time.Second
)

var (
	// Offline-upgrade rollout metrics (meter shared with sandbox.go).
	envdOfflineUpgradeAttempts     = utils.Must(telemetry.GetCounter(meter, telemetry.OrchestratorEnvdOfflineUpgradeAttempts))
	envdOfflineUpgradeDurationHist = utils.Must(telemetry.GetHistogram(meter, telemetry.OrchestratorEnvdOfflineUpgradeDurationName))
)

// RebootSandbox cold-boots a fresh Firecracker VM from the template's rootfs,
// without restoring guest memory. Used to resume filesystem-only snapshots:
// guest RAM, processes, and sockets are lost; only the filesystem survives.
// The sandbox is marked running only after envd is ready, matching
// ResumeSandbox's routing guarantees; endAt is the caller's absolute end time.
// procOpts, if any, adjust the fc.ProcessOptions of the cold boot after the
// defaults are set (e.g. debugging tools forwarding the guest kernel console).
// IMPORTANT: You must Close() the sandbox after you are done with it.
func (f *Factory) RebootSandbox(
	ctx context.Context,
	t template.Template,
	config *Config,
	runtime RuntimeMetadata,
	endAt time.Time,
	apiConfigToStore *orchestrator.SandboxConfig,
	deferMarkRunning bool,
	procOpts ...func(*fc.ProcessOptions),
) (*Sandbox, error) {
	ctx, span := tracer.Start(ctx, "reboot sandbox")
	defer span.End()

	buildID, err := uuid.Parse(t.Files().BuildID)
	if err != nil {
		return nil, fmt.Errorf("parse build ID: %w", err)
	}

	// Safety gate: only filesystem-only snapshots are safe to cold-boot from. A
	// memory snapshot's rootfs may be missing writes that lived only in the
	// guest page cache (restored on a memory resume), so rebooting it would
	// serve an inconsistent disk. Refuse unless the snapshot is marked fs-only.
	meta, err := t.Metadata()
	if err != nil {
		return nil, fmt.Errorf("get template metadata: %w", err)
	}
	if !meta.IsFilesystemOnly() {
		return nil, fmt.Errorf("refusing to reboot build %s: not a filesystem-only snapshot", buildID)
	}

	// A cold boot starts envd with no prior state, so unlike a memory resume it
	// can't inherit the template's default user/workdir from restored RAM — they
	// must be re-sent via /init, or envd falls back to root and /root. Mirror
	// finalize's build-time logic (Context.User, with a "user" fallback for
	// pre-V2 builds that didn't record one).
	if config.Envd.DefaultUser == nil {
		defaultUser := meta.Context.User
		if defaultUser == "" {
			defaultUser = "user"
		}
		config.Envd.DefaultUser = &defaultUser
	}
	if config.Envd.DefaultWorkdir == nil {
		config.Envd.DefaultWorkdir = meta.Context.WorkDir
	}

	// A cold boot also needs Docker image ENV vars from the template metadata.
	// User-provided env vars take precedence over template defaults.
	if len(meta.Context.EnvVars) > 0 {
		merged := make(map[string]string, len(meta.Context.EnvVars)+len(config.Envd.Vars))
		for k, v := range meta.Context.EnvVars {
			merged[k] = v
		}
		for k, v := range config.Envd.Vars {
			merged[k] = v
		}
		config.Envd.Vars = merged
	}

	// The masked empty memfile is used only for sizing NoopMemory — guest RAM
	// is FC's own fresh anonymous memory.
	pageSize := int64(header.PageSize)
	if config.HugePages {
		pageSize = int64(header.HugepageSize)
	}
	memfile, err := block.NewEmpty(units.MBToBytes(config.RamMB), pageSize, buildID)
	if err != nil {
		return nil, fmt.Errorf("create empty memfile: %w", err)
	}

	maskedTemplate := template.NewMaskTemplate(t, template.WithMemfile(memfile))

	kvmClock, err := utils.IsGTEVersion(config.Envd.Version, minEnvdVersionForKVMClock)
	if err != nil {
		return nil, fmt.Errorf("compare envd version: %w", err)
	}

	// Sync IO engine so no async writes are in flight if the sandbox is paused again.
	ioEngine := models.DriveIoEngineSync

	// Always write MMDS metadata for a reboot so the cold-booted envd can
	// authenticate /init like a memory resume. An empty token hashes to the
	// "no token" value, matching ResumeSandbox's behavior for non-secure sandboxes.
	accessToken := ""
	if config.Envd.AccessToken != nil {
		accessToken = *config.Envd.AccessToken
	}

	timeout := time.Until(endAt)
	if timeout <= 0 {
		return nil, fmt.Errorf("sandbox end time %s is in the past", endAt)
	}

	processOptions := fc.ProcessOptions{
		InitScriptPath: constants.SystemdInitPath,
		KvmClock:       kvmClock,
		IoEngine:       &ioEngine,
		AccessToken:    &accessToken,
		// This is a cold boot, so unlike a memory resume it re-reads the command line —
		// and the snapshot's own metadata is the only record of what it was built with.
		// Replayed verbatim rather than re-resolved from the feature flag: what a
		// variant name means can change after the build, so re-resolving would boot a
		// different guest than the one this lineage was created as.
		CmdlineArgs: meta.CmdlineArgs,
	}

	// Recorded so a dropped variant is detectable after the fact: a cold boot carrying
	// the default under a lineage whose build recorded a variant means the field was
	// lost somewhere between build and boot. That is otherwise invisible, because the
	// guest simply comes back without whatever the variant provided.
	//
	// Reports what was APPLIED, not what was stored. buildKernelArgs drops an overlay
	// whose arguments this binary rejects — which is reachable if the reserved set grew
	// since the build that stamped them — so recording the stored name unconditionally
	// would claim a variant the guest never got, defeating the attribute's purpose.
	applied := ""
	if fc.ValidateCmdlineArgs(meta.CmdlineArgs) == nil {
		applied = fc.KernelArgs(meta.CmdlineArgs).String()
	}

	span.SetAttributes(attribute.String("sandbox.cmdline_args", applied))
	for _, opt := range procOpts {
		opt(&processOptions)
	}

	preBoot := f.envdOfflineUpgradePreBoot(ctx, config, runtime, meta.IsFsQuiesced(), buildID)

	sbx, err := f.CreateSandbox(
		ctx,
		config,
		runtime,
		maskedTemplate,
		timeout,
		// Empty rootfs cache path selects the NBD provider, same as a memory
		// resume, so guest TRIM keeps working and a later pause exports the
		// overlay diff exactly like a normal resume.
		"",
		processOptions,
		apiConfigToStore,
		preBoot,
		WithDeferredMarkRunning(),
		withNetworkAssignReason(NetworkAssignReasonReboot),
	)
	if err != nil {
		return nil, fmt.Errorf("create sandbox from rootfs: %w", err)
	}

	// CreateSandbox anchors the lifetime to now; honor the caller's absolute end
	// time so queue delay can't extend the TTL.
	sbx.SetEndAt(endAt)

	if err := sbx.WaitForEnvd(ctx, StartTypeReboot, rebootEnvdTimeout); err != nil {
		closeErr := sbx.Close(context.WithoutCancel(ctx))

		return nil, errors.Join(fmt.Errorf("wait for envd after reboot: %w", err), closeErr)
	}

	// deferMarkRunning: the caller promotes the sandbox to live itself after a
	// post-resume step (the resume-time envd live-upgrade's post-/init), so it is
	// not routable during the upgrade's pre-init auth window. Mirrors the resume
	// path's WithDeferredLiveRegistration.
	if !deferMarkRunning {
		f.Sandboxes.MarkRunning(ctx, sbx)

		go sbx.Checks.Start(context.WithoutCancel(ctx))
	}

	return sbx, nil
}

// offlineSwapDecision is the pure outcome of the offline-upgrade gate: whether to
// rewrite the rootfs envd, and — when not — how to report the no-op.
type offlineSwapDecision struct {
	swap         bool   // run the rootfs swap
	countResult  string // if set, record offline_upgrade.attempts{result=...} for this gated no-op
	logMisconfig bool   // log the resolver's no-op reason (a misconfigured / unstaged target)
}

// offlineNoopNotQuiesced is the countResult for the one no-op that is neither a
// resolver outcome nor an operator error: the resolver wanted an upgrade, but the
// snapshot's rootfs was not frozen at pause.
const offlineNoopNotQuiesced = "not_quiesced"

// decideOfflineSwap gates the cold-boot envd swap on the resolver outcome and the
// snapshot's crash-consistency. resolverPath is "" when the resolver returns no
// upgrade (reason says why); fsQuiesced is whether the rootfs was frozen at pause.
//
//   - flag off (off / no reason)                            -> no swap, silent, uncounted
//   - no upgrade, already on target (same_version)          -> no swap, count it
//   - no upgrade, misconfig (not_staged / downgrade / ...)   -> no swap, count AND log
//   - upgrade wanted but rootfs not frozen                  -> no swap, count not_quiesced
//   - upgrade wanted and rootfs frozen                      -> swap
//
// Every no-op except `off` is counted, so the eligible population adds up: a cold boot
// that did not upgrade is either counted here or counted as an attempt below, and a ramp
// can read what fraction each reason holds. `off` is the deliberate exemption — it is the
// whole fs-only population minus the rest, already available as
// sandbox.create.duration{fs_only="true"}, and counting it would add a series per
// version pair for every cold boot in the fleet to say nothing.
func decideOfflineSwap(resolverPath, reason string, fsQuiesced bool) offlineSwapDecision {
	if resolverPath == "" {
		switch reason {
		case "", "off":
			return offlineSwapDecision{}
		case "same_version":
			// Expected, and the goal state of a ramp — counted so it is visible, not
			// logged because it recurs on every cold boot of an upgraded snapshot.
			return offlineSwapDecision{countResult: reason}
		default:
			// not_staged / downgrade / invalid_target / getversion_failed, and anything
			// the resolver's vocabulary grows: an operator error, so log each one.
			return offlineSwapDecision{countResult: reason, logMisconfig: true}
		}
	}
	if !fsQuiesced {
		return offlineSwapDecision{countResult: offlineNoopNotQuiesced}
	}

	return offlineSwapDecision{swap: true}
}

// envdOfflineUpgradePreBoot returns a PreBootFn that rewrites the rootfs envd
// binary before the cold boot, or nil when no upgrade applies. It
// resolves the target through the shared envd-upgrade decision (ResolveEnvdOfflineUpgrade,
// sibling flag envd-offline-upgrade-target) keyed on the snapshot's built-with
// version — there is no running envd at cold-boot swap time, so unlike the live
// path there is no LiveEnvdVersion to key on, and the built-with never advances
// across an upgrade, so the swap re-fires idempotently on every resume until a
// re-pause re-bakes the version. The swap runs only when the snapshot's rootfs
// was frozen at pause (fs_quiesced): a legacy/sync-fallback snapshot is left on
// its current envd and becomes eligible after its next freezing pause. Fully
// best-effort — any failure boots the ORIGINAL envd, never aborting the boot.
func (f *Factory) envdOfflineUpgradePreBoot(
	ctx context.Context,
	config *Config,
	runtime RuntimeMetadata,
	fsQuiesced bool,
	buildID uuid.UUID,
) PreBootFn {
	from := config.Envd.Version

	sbCtx := featureflags.SandboxContext(runtime.SandboxID)
	tmplCtx := featureflags.TemplateContext(runtime.TemplateID)
	path, toVersion, reason := featureflags.ResolveEnvdOfflineUpgrade(
		ctx, f.featureFlags, from, f.config.HostEnvdPath, buildenvd.GetEnvdVersion, sbCtx, tmplCtx,
	)
	dec := decideOfflineSwap(path, reason, fsQuiesced)
	if !dec.swap {
		// A misconfigured / unstaged target is worth a logged signal on a ramp;
		// off / same_version are the expected per-resume no-ops and stay silent.
		if dec.logMisconfig {
			logger.L().Info(ctx, "offline envd upgrade: target not resolved",
				logger.WithSandboxID(runtime.SandboxID),
				logger.WithBuildID(buildID.String()),
				zap.String("reason", reason),
				zap.String("built_with", from),
			)
		}
		// The resolver wanted an upgrade but the rootfs isn't known crash-consistent
		// (not frozen at pause): don't rewrite it — this population is waiting for a
		// future freezing pause, so it is worth a line each as well as the count.
		if dec.countResult == offlineNoopNotQuiesced {
			logger.L().Info(ctx, "skipping offline envd upgrade: snapshot rootfs was not frozen at pause (not crash-consistent); awaiting a future freezing pause",
				logger.WithSandboxID(runtime.SandboxID),
				logger.WithBuildID(buildID.String()),
				zap.String("built_with", from),
				zap.String("target", path),
				zap.String("to_version", toVersion),
			)
		}
		// One emission for every counted no-op, whatever its kind — the logging above
		// varies by reason, the accounting must not.
		if dec.countResult != "" {
			envdOfflineUpgradeAttempts.Add(ctx, 1, metric.WithAttributes(
				attribute.String("result", dec.countResult),
				attribute.String("from_version", from),
				attribute.String("to_version", toVersion),
			))
		}

		return nil
	}

	return func(ctx context.Context, rootfsPath string) error {
		start := time.Now()
		// Cancel-free but time-bounded: a request cancellation must not kill
		// debugfs mid-write (a half-written inode would break the boot/export that
		// follows), yet a hung tool must not stall the boot forever. The budget is
		// EnvdSwapBudget, not the per-invocation EnvdSwapTimeout: the call is several
		// debugfs runs, and bounding the whole of it at one run's timeout lets a slow
		// backup starve the phases after it (see EnvdSwapBudget).
		swapCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rootfs.EnvdSwapBudget)
		swapped, err := rootfs.SwapEnvdBinary(swapCtx, rootfsPath, path)
		cancel()

		// An unrecoverable swap (failed AND the original was not restored) may leave
		// the rootfs without a usable envd. Booting it would hand back a running but
		// envd-less sandbox; instead fail the boot so CreateSandbox tears down and
		// the dirty overlay is discarded. Every other failure left the original in
		// place, so it stays best-effort and boots the original.
		unrecoverable := errors.Is(err, rootfs.ErrOfflineSwapUnrecoverable)
		result := "success"
		switch {
		case err == nil:
			logger.L().Info(ctx, "swapped envd binary before reboot",
				logger.WithSandboxID(runtime.SandboxID),
				logger.WithBuildID(buildID.String()),
				zap.String("target", path),
				zap.String("to_version", toVersion),
				zap.String("built_with", from),
				// built_with is what the snapshot RECORDS; refire is what the rootfs
				// actually held. They disagree on every re-fire — see SwapResult.Refire.
				zap.Bool("refire", swapped.Refire),
			)
		case errors.Is(err, rootfs.ErrEnvdTooLarge):
			// Declined before anything was touched, so the guest boots its own envd.
			// Its own result value: this is a property of the rootfs, not a malfunction,
			// and on a ramp it should not read as swap breakage.
			result = "envd_too_large"
			logger.L().Warn(ctx, "skipping offline envd upgrade: rootfs envd is too large to swap",
				logger.WithSandboxID(runtime.SandboxID),
				logger.WithBuildID(buildID.String()),
				zap.String("target", path),
				zap.String("built_with", from),
				zap.Error(err),
			)
		case unrecoverable:
			result = "unrecoverable"
			logger.L().Error(ctx, "offline envd swap left the rootfs without a usable envd; failing boot to discard the overlay",
				logger.WithSandboxID(runtime.SandboxID),
				logger.WithBuildID(buildID.String()),
				zap.String("target", path),
				zap.String("built_with", from),
				zap.Error(err),
			)
		default:
			result = "swap_failed"
			logger.L().Error(ctx, "offline envd swap before reboot failed; booting original envd",
				logger.WithSandboxID(runtime.SandboxID),
				logger.WithBuildID(buildID.String()),
				zap.String("target", path),
				zap.String("built_with", from),
				zap.Error(err),
			)
		}

		attrs := []attribute.KeyValue{
			attribute.String("result", result),
			attribute.String("from_version", from),
			attribute.String("to_version", toVersion),
		}
		// from_version is a CLAIM read off the snapshot record, not an observation of the
		// rootfs, so a success count alone overstates how many sandboxes actually moved:
		// an already-upgraded snapshot re-resolves the same upgrade on every cold boot
		// (the record is never advanced) and rewrites the same bytes. refire splits the
		// two. Attached on success only — that is where the comparison is known to have
		// happened, and an absent label beats a false one that means "no idea".
		if err == nil {
			attrs = append(attrs, attribute.Bool("refire", swapped.Refire))
		}
		envdOfflineUpgradeAttempts.Add(ctx, 1, metric.WithAttributes(attrs...))
		envdOfflineUpgradeDurationHist.Record(ctx, time.Since(start).Milliseconds(),
			metric.WithAttributes(attribute.String("result", result)))

		if unrecoverable {
			return fmt.Errorf("offline envd upgrade left rootfs unbootable: %w", err)
		}

		// Best-effort: swallow a recoverable failure so the cold boot proceeds on
		// the original envd rather than failing the whole resume.
		return nil
	}
}
