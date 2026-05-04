package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
)

// StartEnvdProcess opens a streaming Process.Start call against this
// sandbox's envd, running `script` under `/bin/bash -c` as `user`. When
// timeout > 0 it sets `Connect-Timeout-Ms` so envd kills the process
// hard at the deadline. Auth/user headers are wired from sandbox config.
// Caller owns the returned stream (Close + Receive).
func (s *Sandbox) StartEnvdProcess(
	ctx context.Context,
	script, user string,
	timeout time.Duration,
) (*connect.ServerStreamForClient[process.StartResponse], error) {
	addr := fmt.Sprintf("http://%s:%d", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)
	pc := processconnect.NewProcessClient(&http.Client{Transport: sandboxHttpClient.Transport}, addr)

	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{Cmd: "/bin/bash", Args: []string{"-c", script}},
	})
	if timeout > 0 {
		req.Header().Set("Connect-Timeout-Ms", strconv.FormatInt(int64(timeout/time.Millisecond), 10))
	}
	if s.Config.Envd.AccessToken != nil {
		req.Header().Set("X-Access-Token", *s.Config.Envd.AccessToken)
	}
	grpc.SetUserHeader(req.Header(), user)

	return pc.Start(ctx, req)
}
