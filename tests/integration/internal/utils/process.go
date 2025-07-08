package utils

import (
	"context"
	"fmt"
	"testing"

	"connectrpc.com/connect"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func ExecCommand(t *testing.T, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, command string, args ...string) error {
	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  command,
			Args: args,
		},
	})
	setup.SetSandboxHeader(req.Header(), sbx.SandboxID)
	setup.SetUserHeader(req.Header(), "user")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := envdClient.ProcessClient.Start(ctx, req)
	if err != nil {
		return err
	}
	fmt.Printf("Executing command %s in sandbox %s\n", command, sbx.SandboxID)
	defer func() {
		cancel()
		streamErr := stream.Close()
		if streamErr != nil {
			t.Logf("Error closing stream: %v", streamErr)
		}
	}()

	for stream.Receive() {

		select {
		case <-ctx.Done():
			// Context canceled, exit the goroutine
			return ctx.Err()
		default:
			_ = stream.Msg()
		}
	}

	if err := stream.Err(); err != nil {
		return fmt.Errorf("failed to execute command %s in sandbox %s: %w", command, sbx.SandboxID, err)
	}

	return nil
}
