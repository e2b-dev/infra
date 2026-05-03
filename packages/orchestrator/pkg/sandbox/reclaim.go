package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// Steps separated by ';' so each runs even if a previous one fails. On
// timeout envd kills bash; the in-flight syscall finishes and remaining
// steps are skipped.
const reclaimScript = `sync; echo 3 > /proc/sys/vm/drop_caches 2>/dev/null; echo 1 > /proc/sys/vm/compact_memory 2>/dev/null; fstrim -av 2>/dev/null`

// bestEffortReclaim asks envd to reclaim guest memory + disk before pause.
// All failures are swallowed.
func (s *Sandbox) bestEffortReclaim(ctx context.Context, timeout time.Duration) {
	ctx, span := tracer.Start(ctx, "envd-reclaim")
	defer span.End()

	rcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr := fmt.Sprintf("http://%s:%d", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)
	pc := processconnect.NewProcessClient(&http.Client{Transport: sandboxHttpClient.Transport}, addr)

	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{Cmd: "/bin/bash", Args: []string{"-c", reclaimScript}},
	})
	req.Header().Set("Connect-Timeout-Ms", strconv.FormatInt(int64(timeout/time.Millisecond), 10))
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
