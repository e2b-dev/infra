//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Slack covers shell start + envd round-trip overhead.
const reclaimOuterSlack = 500 * time.Millisecond

// freezeTimeout bounds the native POST /freeze call. The freeze itself is a
// single sysfs write but envd may be slow to schedule under load, so this
// must stay independent of the reclaim shell deadline.
const freezeTimeout = 2 * time.Second

const (
	// syncMinTimeout floors the guest-sync deadline; it covers small-RAM
	// sandboxes and the shell round-trip.
	syncMinTimeout = 5 * time.Second

	// syncMaxTimeout caps the guest-sync deadline so a stuck sync still fails the
	// pause in bounded time rather than hanging it.
	syncMaxTimeout = 2 * time.Minute

	// syncFlushFloorBytesPerSec is a pessimistic floor for guest page-cache
	// flush throughput to the virtio disk under IO contention. The data a sync
	// must flush is bounded by the dirty page cache (≈ guest RAM; pages already
	// written back are not re-flushed), so the deadline scales with RAM against
	// this floor. Conservative on purpose: too low only over-waits, while too
	// high would falsely fail the (mandatory) pre-pause sync.
	syncFlushFloorBytesPerSec = 50 * 1024 * 1024
)

// buildReclaimScript builds the fstrim/sync/drop_caches/compact_memory chain.
// Returns ("", 0) when every step is disabled.
func (s *Sandbox) buildReclaimScript(cfg featureflags.ReclaimConfig) (string, time.Duration) {
	var (
		parts []string
		sum   time.Duration
	)

	steps := []struct {
		cap time.Duration
		cmd string
	}{
		{cfg.Fstrim, "fstrim -av"},
		{cfg.Sync, "sync"},
		{cfg.DropCaches, "echo 3 > /proc/sys/vm/drop_caches"},
		{cfg.CompactMemory, "echo 1 > /proc/sys/vm/compact_memory"},
	}

	for _, st := range steps {
		// %.3f at <1ms renders as 0.000 → GNU timeout reads as "no timeout".
		if st.cap < time.Millisecond {
			continue
		}
		parts = append(parts, fmt.Sprintf("timeout -s KILL %.3f sh -c %q >/dev/null 2>&1 || rc=$?", st.cap.Seconds(), st.cmd))
		sum += st.cap
	}
	if len(parts) == 0 {
		return "", 0
	}

	return "rc=0; " + strings.Join(parts, "; ") + "; exit $rc", sum + reclaimOuterSlack
}

// bestEffortReclaim optionally freezes user cgroups, then runs the
// fstrim/sync/drop_caches/compact_memory chain via envd before pause.
func (s *Sandbox) bestEffortReclaim(ctx context.Context) {
	ctx, span := tracer.Start(ctx, "envd-reclaim")
	defer span.End()

	ctx = featureflags.AddToContext(
		ctx,
		sandboxLDContext(s.Runtime, s.Config),
		featureflags.TeamContext(s.Runtime.TeamID),
		featureflags.TemplateContext(s.Runtime.TemplateID),
	)

	if s.featureFlags.BoolFlag(ctx, featureflags.FreezeUserCgroupFlag) {
		s.bestEffortFreeze(ctx)
	}

	if s.featureFlags.BoolFlag(ctx, featureflags.CollapseEnvdHeapFlag) {
		s.bestEffortCollapse(ctx)
	}

	cfg := featureflags.GetReclaimConfig(ctx, s.featureFlags)
	script, timeout := s.buildReclaimScript(cfg)
	if script == "" {
		return
	}

	rcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stream, err := s.StartEnvdSystemShell(rcCtx, "/bin/sh", []string{"-c", script}, "root", timeout)
	if err != nil {
		logger.L().Warn(ctx, "envd reclaim failed", logger.WithSandboxID(s.Runtime.SandboxID), zap.Error(err))

		return
	}
	defer stream.Close()

	var exitCode int32
	for stream.Receive() {
		if end := stream.Msg().GetEvent().GetEnd(); end != nil {
			exitCode = end.GetExitCode()
		}
	}
	if err := stream.Err(); err != nil {
		logger.L().Warn(ctx, "envd reclaim stream error", logger.WithSandboxID(s.Runtime.SandboxID), zap.Error(err))

		return
	}
	if exitCode != 0 {
		logger.L().Warn(ctx, "envd reclaim non-zero exit", logger.WithSandboxID(s.Runtime.SandboxID), zap.Int32("exit_code", exitCode))
	}
}

// ramScaledSyncTimeout derives the guest-sync deadline from guest RAM. The
// dirty page cache that sync must flush is bounded by RAM, divided by a
// pessimistic flush-throughput floor, then clamped to
// [syncMinTimeout, syncMaxTimeout].
func ramScaledSyncTimeout(ramMB int64) time.Duration {
	ramBytes := ramMB * 1024 * 1024
	d := time.Duration(ramBytes/syncFlushFloorBytesPerSec) * time.Second

	if d < syncMinTimeout {
		return syncMinTimeout
	}
	if d > syncMaxTimeout {
		return syncMaxTimeout
	}

	return d
}

// guestSyncTimeout returns the deadline for the pre-pause guest sync. The
// GuestSyncTimeoutMs feature flag pins it (milliseconds) when set to a positive
// value; otherwise it scales with guest RAM via ramScaledSyncTimeout.
func (s *Sandbox) guestSyncTimeout(ctx context.Context) time.Duration {
	if ms := s.featureFlags.IntFlag(ctx, featureflags.GuestSyncTimeoutMs,
		featureflags.SandboxContext(s.Runtime.SandboxID),
		featureflags.TeamContext(s.Runtime.TeamID),
		featureflags.TemplateContext(s.Runtime.TemplateID),
	); ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}

	return ramScaledSyncTimeout(s.Config.RamMB)
}

func (s *Sandbox) guestPrepareFsForPause(ctx context.Context, cleanup *Cleanup) (e error) {
	supportsFsFreeze := s.envdSupportsFsFreeze(ctx)
	// Use guestSyncTimeout here, as fsfreeze also syncs the disk
	timeout := s.guestSyncTimeout(ctx)
	start := time.Now()

	ctx, span := tracer.Start(
		ctx,
		"envd-guest-fs-pause",
		trace.WithAttributes(attribute.Bool("fsfreeze", supportsFsFreeze)),
	)
	defer span.End()

	// Record on every exit so slow and timed-out syncs are captured too.
	defer func() {
		guestSyncDurationHistogram.Record(ctx, time.Since(start).Milliseconds(),
			metric.WithAttributes(
				attribute.Bool("success", e == nil),
				attribute.Bool("fsfreeze", supportsFsFreeze),
				attribute.Int64("timeout_ms", timeout.Milliseconds()),
			),
		)
	}()

	if supportsFsFreeze {
		// fsfreeze flushes the rootfs AND blocks further writes until thaw,
		// closing the sync->pause race. FIFREEZE already syncs as part of
		// freezing, so a separate guest sync would be redundant.
		// If freezing aborted, thaw so we don't leave the live VM's
		// filesystem frozen; on success the VM is stopped during rootfs
		// export, so the frozen state is discarded with it and the thaw is a
		// harmless no-op.
		cleanup.Add(ctx, func(ctx context.Context) error {
			s.bestEffortFsthaw(ctx)

			return nil
		})
		if err := s.callEnvdFsfreeze(ctx, timeout); err != nil {
			return fmt.Errorf("fsfreeze before filesystem-only pause: %w", err)
		}
	} else {
		if err := s.guestSync(ctx, timeout); err != nil {
			return fmt.Errorf("guest sync before filesystem-only pause: %w", err)
		}
	}

	return nil
}

// guestSync runs sync in the guest via envd so ext4 flushes dirty pages to the
// virtio disk. Mandatory before a filesystem-only pause: without a memory
// snapshot the guest page cache is lost, so callers must fail the pause on
// error instead of persisting a rootfs missing acknowledged writes. Unlike
// bestEffortReclaim's sync step (LD-flag gated, best-effort), this always runs
// and always reports failure.
func (s *Sandbox) guestSync(ctx context.Context, syncTimeout time.Duration) (e error) {
	rcCtx, cancel := context.WithTimeout(ctx, syncTimeout+reclaimOuterSlack)
	defer cancel()

	stream, err := s.StartEnvdSystemShell(rcCtx, "/bin/sh", []string{"-c", "sync"}, "root", syncTimeout)
	if err != nil {
		return fmt.Errorf("start guest sync: %w", err)
	}
	defer stream.Close()

	exitCode := int32(-1)
	for stream.Receive() {
		if end := stream.Msg().GetEvent().GetEnd(); end != nil {
			exitCode = end.GetExitCode()
		}
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("guest sync stream: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("guest sync exited with code %d", exitCode)
	}

	return nil
}

// envdSupportsCgroupFreeze reports whether the sandbox's envd exposes the
// native /freeze and /unfreeze endpoints. Bad version strings log and return
// false so we never accidentally call an unsupported endpoint.
func (s *Sandbox) envdSupportsCgroupFreeze(ctx context.Context) bool {
	ok, err := utils.IsGTEVersion(s.Config.Envd.Version, utils.MinEnvdVersionForCgroupFreeze)
	if err != nil {
		logger.L().Warn(ctx, "cgroup freeze version gate: bad envd version", logger.WithSandboxID(s.Runtime.SandboxID), zap.String("envd_version", s.Config.Envd.Version), zap.Error(err))

		return false
	}

	return ok
}

// envdSupportsFsFreeze reports whether the sandbox's envd exposes the native
// /fsfreeze and /fsthaw endpoints. Bad version strings log and return false so
// the filesystem-only pause falls back to a plain guest sync.
func (s *Sandbox) envdSupportsFsFreeze(ctx context.Context) bool {
	ok, err := utils.IsGTEVersion(s.Config.Envd.Version, utils.MinEnvdVersionForFsFreeze)
	if err != nil {
		logger.L().Warn(ctx, "fsfreeze version gate: bad envd version", logger.WithSandboxID(s.Runtime.SandboxID), zap.String("envd_version", s.Config.Envd.Version), zap.Error(err))

		return false
	}

	return ok
}

// envdSupportsHeapCollapse reports whether the sandbox's envd exposes the native
// /collapse endpoint. Bad version strings log and return false so we never call
// an unsupported endpoint.
func (s *Sandbox) envdSupportsHeapCollapse(ctx context.Context) bool {
	ok, err := utils.IsGTEVersion(s.Config.Envd.Version, utils.MinEnvdVersionForHeapCollapse)
	if err != nil {
		logger.L().Warn(ctx, "heap collapse version gate: bad envd version", logger.WithSandboxID(s.Runtime.SandboxID), zap.String("envd_version", s.Config.Envd.Version), zap.Error(err))

		return false
	}

	return ok
}

// bestEffortCollapse asks envd to collapse its own heap into hugepages before
// pause, so on resume envd touches fewer distinct frames. Gated on envd version;
// failures are logged but never block pause.
func (s *Sandbox) bestEffortCollapse(ctx context.Context) {
	if !s.envdSupportsHeapCollapse(ctx) {
		return
	}

	ctx, span := tracer.Start(ctx, "envd-collapse")
	defer span.End()

	// Timeout comes straight from the flag, whose fallback (10s) is returned
	// whenever LD is unavailable or the flag is unset — so there is no separate
	// local default to keep in sync.
	timeout := time.Duration(s.featureFlags.IntFlag(ctx, featureflags.CollapseEnvdHeapTimeoutMsFlag)) * time.Millisecond

	start := time.Now()
	stats, err := s.callEnvdCollapse(ctx, timeout)
	elapsedMs := time.Since(start).Milliseconds()
	success := err == nil

	// Record the round-trip duration whether or not it succeeded: a timed-out or
	// failed collapse still spends time on the pause path and must be visible.
	envdCollapseDurationHistogram.Record(ctx, elapsedMs, metric.WithAttributes(attribute.Bool("success", success)))
	span.SetAttributes(
		attribute.Bool("collapse.success", success),
		attribute.Int64("collapse.duration_ms", elapsedMs),
	)

	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		logger.L().Warn(ctx, "envd heap collapse failed", logger.WithSandboxID(s.Runtime.SandboxID), zap.Error(err))

		return
	}

	// Chunk-level efficacy split three ways so the dashboard can separate real
	// work from no-ops: attempts = collapsed + already_huge + skipped, where
	// collapsed = pages actually migrated this pause, already_huge = windows that
	// were already hugepages (MADV_COLLAPSE succeeded but did nothing).
	envdCollapseChunks.Add(ctx, int64(stats.Collapsed), metric.WithAttributes(attribute.String("result", "collapsed")))
	envdCollapseChunks.Add(ctx, int64(stats.AlreadyHuge), metric.WithAttributes(attribute.String("result", "already_huge")))
	envdCollapseChunks.Add(ctx, int64(stats.Skipped), metric.WithAttributes(attribute.String("result", "skipped")))
	span.SetAttributes(
		attribute.Int("collapse.regions", stats.Regions),
		attribute.Int("collapse.chunks", stats.Chunks),
		attribute.Int("collapse.collapsed", stats.Collapsed),
		attribute.Int("collapse.already_huge", stats.AlreadyHuge),
		attribute.Int("collapse.skipped", stats.Skipped),
	)

	logger.L().Info(ctx, "envd heap collapsed",
		logger.WithSandboxID(s.Runtime.SandboxID),
		zap.Int("regions", stats.Regions),
		zap.Int("chunks", stats.Chunks),
		zap.Int("collapsed", stats.Collapsed),
		zap.Int("already_huge", stats.AlreadyHuge),
		zap.Int("skipped", stats.Skipped),
		zap.Int64("duration_ms", elapsedMs),
	)
}

// bestEffortFreeze calls envd's native /freeze endpoint with a tight, freeze-
// only deadline so it doesn't share a timeout budget with the rest of reclaim.
// Gated on envd version; failures are logged but never block pause.
func (s *Sandbox) bestEffortFreeze(ctx context.Context) {
	if !s.envdSupportsCgroupFreeze(ctx) {
		return
	}

	if err := s.callEnvdFreeze(ctx, freezeTimeout); err != nil {
		logger.L().Warn(ctx, "envd freeze failed", logger.WithSandboxID(s.Runtime.SandboxID), zap.Error(err))
	}
}

// bestEffortUnfreeze calls envd's native /unfreeze endpoint with a tight
// deadline. Reserved for the Pause error-cleanup chain so a failed pause
// doesn't leave a live sandbox permanently frozen; the resume thaw is handled
// by /init's defer and must not be moved here. Gated on envd version;
// failures are logged. Uses context.WithoutCancel because callers run it from
// cleanup paths whose parent ctx may already be done.
func (s *Sandbox) bestEffortUnfreeze(ctx context.Context) {
	if !s.envdSupportsCgroupFreeze(ctx) {
		return
	}

	if err := s.callEnvdUnfreeze(context.WithoutCancel(ctx), freezeTimeout); err != nil {
		logger.L().Warn(ctx, "envd unfreeze failed", logger.WithSandboxID(s.Runtime.SandboxID), zap.Error(err))
	}
}

// bestEffortFsthaw thaws the guest rootfs via envd's native /fsthaw endpoint.
// Reserved for the filesystem-only pause error path so an aborted pause can't
// leave the live VM's filesystem frozen. Gated on envd version; failures are
// logged. Uses context.WithoutCancel because it runs from cleanup paths whose
// parent ctx may already be done.
func (s *Sandbox) bestEffortFsthaw(ctx context.Context) {
	if !s.envdSupportsFsFreeze(ctx) {
		return
	}

	if err := s.callEnvdFsthaw(context.WithoutCancel(ctx), s.guestSyncTimeout(ctx)); err != nil {
		logger.L().Warn(ctx, "envd fsthaw failed", logger.WithSandboxID(s.Runtime.SandboxID), zap.Error(err))
	}
}
