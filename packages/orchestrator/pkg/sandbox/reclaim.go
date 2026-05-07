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

// Slack added to the sum of per-step caps to absorb shell start /
// envd round-trip overhead.
const reclaimOuterSlack = 500 * time.Millisecond

// buildReclaimScript composes a chain where each step has its own
// `timeout -s KILL` ceiling. Step caps come from a single LD JSON flag
// (`reclaim-config`), evaluated against sandbox/team/template contexts so
// targeting is configured in LaunchDarkly. Sub-ms caps are skipped.
// Returns ("", 0) when every step is disabled.
//
// Order matters: sync makes drop_caches more effective; drop_caches gives
// compact_memory more headroom; fstrim wants a stable FS view.
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
		{cfg.Sync, "sync"},
		{cfg.DropCaches, "echo 3 > /proc/sys/vm/drop_caches"},
		{cfg.CompactMemory, "echo 1 > /proc/sys/vm/compact_memory"},
		{cfg.Fstrim, "fstrim -av"},
	}

	var (
		parts []string
		sum   time.Duration
	)
	for _, st := range steps {
		// Skip sub-ms values: `%.3f` would render them as 0.000 which GNU
		// `timeout` treats as "no timeout" (waits forever).
		if st.cap < time.Millisecond {
			continue
		}
		// `timeout` accepts fractional seconds (s/m/h/d), not `ms`. Output
		// is dropped; non-zero status is captured into `rc` so the final
		// exit code surfaces failures without short-circuiting later steps.
		parts = append(parts, fmt.Sprintf("timeout -s KILL %.3f sh -c %q >/dev/null 2>&1 || rc=$?", st.cap.Seconds(), st.cmd))
		sum += st.cap
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
