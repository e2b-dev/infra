//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"strings"
	"time"

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

	cfg := featureflags.GetReclaimConfig(ctx, s.featureFlags,
		featureflags.SandboxContext(s.Runtime.SandboxID),
		featureflags.TeamContext(s.Runtime.TeamID),
		featureflags.TemplateContext(s.Runtime.TemplateID),
	)

	if s.featureFlags.BoolFlag(ctx, featureflags.FreezeUserCgroupFlag,
		featureflags.SandboxContext(s.Runtime.SandboxID),
		featureflags.TeamContext(s.Runtime.TeamID),
		featureflags.TemplateContext(s.Runtime.TemplateID),
	) {
		s.bestEffortFreeze(ctx)
	}

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

// bestEffortGuestSync runs sync in the guest via envd so ext4 flushes dirty
// pages to the virtio disk before a filesystem-only pause.
func (s *Sandbox) bestEffortGuestSync(ctx context.Context) {
	const syncTimeout = 5 * time.Second

	ctx, span := tracer.Start(ctx, "envd-guest-sync")
	defer span.End()

	rcCtx, cancel := context.WithTimeout(ctx, syncTimeout+reclaimOuterSlack)
	defer cancel()

	stream, err := s.StartEnvdSystemShell(rcCtx, "/bin/sh", []string{"-c", "sync"}, "root", syncTimeout)
	if err != nil {
		logger.L().Warn(ctx, "envd guest sync failed", logger.WithSandboxID(s.Runtime.SandboxID), zap.Error(err))

		return
	}
	defer stream.Close()

	for stream.Receive() {
	}
	if err := stream.Err(); err != nil {
		logger.L().Warn(ctx, "envd guest sync stream error", logger.WithSandboxID(s.Runtime.SandboxID), zap.Error(err))
	}
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
