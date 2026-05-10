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
)

// Slack covers shell start + envd round-trip overhead.
const reclaimOuterSlack = 500 * time.Millisecond

// Order: fstrim → sync → drop_caches → compact_memory. fstrim runs first so
// that the fs metadata it pulls into the page cache and the superblock dirties
// (last-trim timestamps) get flushed by sync and evicted by drop_caches in the
// same pass; compact_memory then consolidates the minimal RSS so the snapshot
// has long contiguous zero runs that compress well. Each step is disabled at
// sub-ms cap. Returns ("", 0) when every step is disabled.
func (s *Sandbox) buildReclaimScript(ctx context.Context) (string, time.Duration) {
	cfg := featureflags.GetReclaimConfig(ctx, s.featureFlags,
		featureflags.SandboxContext(s.Runtime.SandboxID),
		featureflags.TeamContext(s.Runtime.TeamID),
		featureflags.TemplateContext(s.Runtime.TemplateID),
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

	var (
		parts []string
		sum   time.Duration
	)
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

// bestEffortReclaim runs the reclaim chain via envd before pause.
func (s *Sandbox) bestEffortReclaim(ctx context.Context) {
	script, timeout := s.buildReclaimScript(ctx)
	if script == "" {
		return
	}

	ctx, span := tracer.Start(ctx, "envd-reclaim")
	defer span.End()

	rcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stream, err := s.StartEnvdShell(rcCtx, "/bin/sh", []string{"-c", script}, "root", timeout)
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
