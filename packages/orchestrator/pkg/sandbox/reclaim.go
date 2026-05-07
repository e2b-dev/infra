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
	flag featureflags.DurationFlag
	cmd  string
}

// Order matters: sync makes drop_caches more effective; drop_caches gives
// compact_memory more headroom; fstrim wants a stable FS view.
var reclaimSteps = []reclaimStep{
	{featureflags.ReclaimSyncTimeout, "sync"},
	{featureflags.ReclaimDropCachesTimeout, "echo 3 > /proc/sys/vm/drop_caches"},
	{featureflags.ReclaimCompactMemoryTimeout, "echo 1 > /proc/sys/vm/compact_memory"},
	{featureflags.ReclaimFstrimTimeout, "fstrim -av"},
}

// Slack added to the sum of per-step caps to absorb shell start /
// envd round-trip overhead.
const reclaimOuterSlack = 500 * time.Millisecond

// buildReclaimScript composes a chain where each step has its own
// `timeout -s KILL` ceiling. Steps with cap=0 are skipped. Returns
// ("", 0) when every step is disabled.
func (s *Sandbox) buildReclaimScript(ctx context.Context) (string, time.Duration) {
	var (
		parts []string
		sum   time.Duration
	)
	for _, st := range reclaimSteps {
		d := s.featureFlags.DurationFlag(ctx, st.flag)
		if d <= 0 {
			continue
		}
		// `timeout` accepts fractional seconds (s/m/h/d), not `ms`. Output
		// is dropped; non-zero status is captured into `rc` so the final
		// exit code surfaces failures without short-circuiting later steps.
		// Ensure sub-millisecond durations round up to 0.001 so timeout != 0.
		sec := d.Seconds()
		if sec < 0.001 && sec > 0 {
			sec = 0.001
		}
		parts = append(parts, fmt.Sprintf("timeout -s KILL %.3f sh -c %q >/dev/null 2>&1 || rc=$?", sec, st.cmd))
		sum += d
	}
	if len(parts) == 0 {
		return "", 0
	}

	return "rc=0; " + strings.Join(parts, "; ") + "; exit $rc", sum + reclaimOuterSlack
}

// bestEffortReclaim asks envd to reclaim guest memory + disk before pause.
// Per-step output is silenced inside the guest; we only log when envd
// itself errors or the script reports a non-zero exit code.
func (s *Sandbox) bestEffortReclaim(ctx context.Context) {
	script, timeout := s.buildReclaimScript(ctx)
	if script == "" {
		return
	}

	ctx, span := tracer.Start(ctx, "envd-reclaim")
	defer span.End()

	rcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stream, err := s.StartEnvdShell(rcCtx, []string{"-c", script}, "root", timeout)
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
