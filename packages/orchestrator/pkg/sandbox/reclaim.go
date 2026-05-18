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

// freezeCgroupCmd freezes user/ptys/socats. Names must match envd's cgroup
// config; the shell runs in the system cgroup so it's not affected by the freeze.
const freezeCgroupCmd = "for cg in user ptys socats; do " +
	`echo 1 > /sys/fs/cgroup/"$cg"/cgroup.freeze 2>/dev/null || true; ` +
	"done"

// Order: freeze → fstrim → sync → drop_caches → compact_memory. Freeze stops
// new dirty pages so reclaim works on a quiet fs; the frozen state persists
// into the snapshot and envd unfreezes at the end of /init on resume.
// Returns ("", 0) when every step is disabled.
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

	// Gate on envd version: older envds wouldn't unfreeze on resume.
	canFreeze, err := utils.IsGTEVersion(s.Config.Envd.Version, utils.MinEnvdVersionForCgroupFreeze)
	if err != nil {
		logger.L().Warn(ctx, "cgroup freeze version gate: bad envd version", logger.WithSandboxID(s.Runtime.SandboxID), zap.String("envd_version", s.Config.Envd.Version), zap.Error(err))
	}
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
// The shell uses the _system tag so the freeze doesn't catch the shell itself.
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
