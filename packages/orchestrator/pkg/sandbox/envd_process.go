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

// StartEnvdBash opens a streaming Process.Start call against this
// sandbox's envd, running `/bin/sh` with the given args as `user`.
// Caller chooses login-shell vs. plain (e.g. []{"-l","-c",cmd} vs.
// []{"-c",script}). When timeout > 0 it sets `Connect-Timeout-Ms` so
// envd kills the process at the deadline. Auth/user headers are wired
// from sandbox config. Caller owns the returned stream.
func (s *Sandbox) StartEnvdBash(
	ctx context.Context,
	bashArgs []string,
	user string,
	timeout time.Duration,
) (*connect.ServerStreamForClient[process.StartResponse], error) {
	addr := fmt.Sprintf("http://%s:%d", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)
	pc := processconnect.NewProcessClient(&http.Client{Transport: sandboxHttpClient.Transport}, addr)

	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{Cmd: "/bin/sh", Args: bashArgs},
	})
	if timeout > 0 {
		req.Header().Set("Connect-Timeout-Ms", strconv.FormatInt(timeout.Milliseconds(), 10))
	}
	if s.Config.Envd.AccessToken != nil {
		req.Header().Set("X-Access-Token", *s.Config.Envd.AccessToken)
	}
	grpc.SetUserHeader(req.Header(), user)

	return pc.Start(ctx, req)
}
