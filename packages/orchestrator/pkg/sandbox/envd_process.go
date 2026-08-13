//go:build linux

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

// StartEnvdShell opens a streaming Process.Start call against the sandbox's
// envd. timeout > 0 sets Connect-Timeout-Ms so envd kills the process at
// the deadline. Caller owns the returned stream.
func (s *Sandbox) StartEnvdShell(
	ctx context.Context,
	shell string,
	shellArgs []string,
	user string,
	timeout time.Duration,
) (*connect.ServerStreamForClient[process.StartResponse], error) {
	return s.startEnvdProcess(ctx, &process.ProcessConfig{Cmd: shell, Args: shellArgs}, user, timeout, nil)
}

// StartEnvdSystemShell runs the process in envd's root cgroup via the SystemTag.
func (s *Sandbox) StartEnvdSystemShell(
	ctx context.Context,
	shell string,
	shellArgs []string,
	user string,
	timeout time.Duration,
) (*connect.ServerStreamForClient[process.StartResponse], error) {
	tag := consts.SystemTag

	return s.startEnvdProcess(ctx, &process.ProcessConfig{Cmd: shell, Args: shellArgs}, user, timeout, &tag)
}

// StartEnvdBackgroundCommand starts a template-owned long-running command and
// returns after envd confirms that the process exists. Envd deliberately
// detaches process lifetime from the request context, so closing the stream
// after the start event does not terminate the command.
func (s *Sandbox) StartEnvdBackgroundCommand(
	ctx context.Context,
	command string,
	cwd *string,
	envs map[string]string,
	user string,
) error {
	stream, err := s.startEnvdProcess(ctx, &process.ProcessConfig{
		Cmd:  "/bin/bash",
		Cwd:  cwd,
		Args: []string{"-l", "-c", command},
		Envs: envs,
	}, user, 0, nil)
	if err != nil {
		return err
	}
	defer stream.Close()

	for stream.Receive() {
		event := stream.Msg().GetEvent()
		if event == nil {
			continue
		}
		if event.GetStart() != nil {
			return nil
		}
		if end := event.GetEnd(); end != nil {
			return fmt.Errorf("background command exited before its start was confirmed with code %d", end.GetExitCode())
		}
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("background command start stream: %w", err)
	}

	return fmt.Errorf("background command start stream closed before confirmation")
}

func (s *Sandbox) startEnvdProcess(
	ctx context.Context,
	config *process.ProcessConfig,
	user string,
	timeout time.Duration,
	tag *string,
) (*connect.ServerStreamForClient[process.StartResponse], error) {
	addr := fmt.Sprintf("http://%s:%d", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)
	pc := processconnect.NewProcessClient(&http.Client{Transport: sandboxHttpClient.Transport}, addr)

	req := connect.NewRequest(&process.StartRequest{
		Process: config,
		Tag:     tag,
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
