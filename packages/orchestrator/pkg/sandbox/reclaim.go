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

type reclaimStep struct {
	flag featureflags.IntFlag
	cmd  string
}

// Order matters: sync makes drop_caches more effective; drop_caches gives
// compact_memory more headroom; fstrim wants a stable FS view.
var reclaimSteps = []reclaimStep{
	{featureflags.ReclaimSyncTimeoutMs, "sync"},
	{featureflags.ReclaimDropCachesTimeoutMs, "echo 3 > /proc/sys/vm/drop_caches"},
	{featureflags.ReclaimCompactMemoryTimeoutMs, "echo 1 > /proc/sys/vm/compact_memory"},
	{featureflags.ReclaimFstrimTimeoutMs, "fstrim -av"},
}

// Slack added to the sum of per-step caps to absorb shell start /
// envd round-trip overhead.
const reclaimOuterSlack = 500 * time.Millisecond

// buildReclaimScript composes a chain where each step has its own
// `timeout --foreground -s KILL` ceiling. Steps with cap=0 are skipped.
// Returns ("", 0) when every step is disabled.
func (s *Sandbox) buildReclaimScript(ctx context.Context) (string, time.Duration) {
	var (
		parts []string
		sum   time.Duration
	)
	for _, st := range reclaimSteps {
		ms := s.featureFlags.IntFlag(ctx, st.flag)
		if ms <= 0 {
			continue
		}
		// `timeout` accepts fractional seconds (s/m/h/d), not `ms`.
		// `--foreground` ensures SIGKILL actually reaches the child when we
		// run inside a non-interactive bash invoked from envd. Per-step
		// stdout/stderr is dropped; any non-zero status is captured into
		// `rc` so the script's overall exit code surfaces failures without
		// short-circuiting subsequent steps.
		secs := float64(ms) / 1000.0
		parts = append(parts, fmt.Sprintf("timeout --foreground -s KILL %.3f sh -c %q >/dev/null 2>&1 || rc=$?", secs, st.cmd))
		sum += time.Duration(ms) * time.Millisecond
	}
	if len(parts) == 0 {
		return "", 0
	}

	return "rc=0; " + strings.Join(parts, "; ") + "; exit $rc", sum + reclaimOuterSlack
}

// bestEffortReclaim asks envd to reclaim guest memory + disk before pause.
// Per-step stdout/stderr is silenced inside the guest; we only log when
// envd itself errors or the script reports a non-zero exit code.
func (s *Sandbox) bestEffortReclaim(ctx context.Context) {
	ctx, span := tracer.Start(ctx, "envd-reclaim")
	defer span.End()

	script, timeout := s.buildReclaimScript(ctx)
	if script == "" {
		return
	}

	rcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stream, err := s.StartEnvdProcess(rcCtx, script, "root", timeout)
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
