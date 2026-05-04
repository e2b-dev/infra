package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
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

// buildReclaimScript composes a chain where each step has its own hard ceiling
// (`timeout -s KILL`). Steps with cap=0 are skipped; the script is empty when
// every step is disabled.
func (s *Sandbox) buildReclaimScript(ctx context.Context) string {
	var parts []string
	for _, st := range reclaimSteps {
		ms := s.featureFlags.IntFlag(ctx, st.flag)
		if ms <= 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("timeout -s KILL %dms sh -c %q 2>/dev/null; true", ms, st.cmd))
	}

	return strings.Join(parts, " ")
}

// bestEffortReclaim asks envd to reclaim guest memory + disk before pause.
// All failures are swallowed.
func (s *Sandbox) bestEffortReclaim(ctx context.Context, timeout time.Duration) {
	ctx, span := tracer.Start(ctx, "envd-reclaim")
	defer span.End()

	script := s.buildReclaimScript(ctx)
	if script == "" {
		return
	}

	rcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr := fmt.Sprintf("http://%s:%d", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)
	pc := processconnect.NewProcessClient(&http.Client{Transport: sandboxHttpClient.Transport}, addr)

	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{Cmd: "/bin/bash", Args: []string{"-c", script}},
	})
	req.Header().Set("Connect-Timeout-Ms", strconv.FormatInt(int64(timeout/time.Millisecond), 10))
	if s.Config.Envd.AccessToken != nil {
		req.Header().Set("X-Access-Token", *s.Config.Envd.AccessToken)
	}
	grpc.SetUserHeader(req.Header(), "root")

	stream, err := pc.Start(rcCtx, req)
	if err != nil {
		logger.L().Warn(ctx, "envd reclaim failed", logger.WithSandboxID(s.Runtime.SandboxID), zap.Error(err))

		return
	}
	defer stream.Close()

	for stream.Receive() {
	}
}
