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

// freezeCgroupCmd freezes all three managed cgroups (user, ptys, socat).
// Errors are swallowed with || true so that the script continues even if
// cgroup.freeze doesn't exist (old kernel / missing cgroup).
const freezeCgroupCmd = "for cg in user ptys socats; do " +
	`echo 1 > /sys/fs/cgroup/"$cg"/cgroup.freeze 2>/dev/null || true; ` +
	"done"

// Order: freeze → fstrim → sync → drop_caches → compact_memory.
//
// Freeze is first: it stops user/pty/socat processes from generating new dirty
// pages so that the subsequent reclaim steps operate on a quiet filesystem.
// fstrim runs next so that the fs metadata it pulls into the page cache and the
// superblock dirties (last-trim timestamps) get flushed by sync and evicted by
// drop_caches in the same pass; compact_memory then consolidates the minimal
// RSS so the snapshot has long contiguous zero runs that compress well.
//
// The frozen state persists across the Firecracker snapshot. On resume, envd
// unfreezes all three cgroups at the end of /init SetData, eliminating I/O
// contention during initialization.
//
// The shell runs in the "system" cgroup (envd's root cgroup) via the _system
// tag, so it is not affected by the freeze.
//
// Each reclaim step is disabled at sub-ms cap. Returns ("", 0) when every step
// (including freeze) is disabled.
func (s *Sandbox) buildReclaimScript(ctx context.Context) (string, time.Duration) {
	cfg := featureflags.GetReclaimConfig(ctx, s.featureFlags,
		featureflags.SandboxContext(s.Runtime.SandboxID),
		featureflags.TeamContext(s.Runtime.TeamID),
		featureflags.TemplateContext(s.Runtime.TemplateID),
	)

	var (
		parts []string
		sum   time.Duration
	)

	// Freeze all managed cgroups first to stop new dirty pages before reclaim.
	// Only freeze if the envd version supports unfreezing at the end of /init,
	// otherwise the cgroups would stay frozen permanently after resume.
	canFreeze, _ := utils.IsGTEVersion(s.Config.Envd.Version, utils.MinEnvdVersionForCgroupFreeze)
	if cfg.FreezeUserCgroup && canFreeze {
		parts = append(parts, freezeCgroupCmd)
	}

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

// bestEffortReclaim runs the freeze + reclaim chain via envd before pause.
//
// The shell is spawned with the _system tag so it runs in envd's root cgroup,
// unaffected by the cgroup freeze it performs.
func (s *Sandbox) bestEffortReclaim(ctx context.Context) {
	script, timeout := s.buildReclaimScript(ctx)
	if script == "" {
		return
	}

	ctx, span := tracer.Start(ctx, "envd-reclaim")
	defer span.End()

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
