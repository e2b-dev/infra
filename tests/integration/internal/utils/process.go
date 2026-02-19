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

func ExecCommand(tb testing.TB, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, command string, args ...string) error {
	tb.Helper()

	return ExecCommandWithOptions(tb, ctx, sbx, envdClient, nil, "user", command, args...)
}

func ExecCommandWithCwd(tb testing.TB, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, cwd *string, command string, args ...string) error {
	tb.Helper()

	return ExecCommandWithOptions(tb, ctx, sbx, envdClient, cwd, "user", command, args...)
}

func ExecCommandAsRoot(tb testing.TB, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, command string, args ...string) error {
	tb.Helper()

	return ExecCommandWithOptions(tb, ctx, sbx, envdClient, nil, "root", command, args...)
}

func ExecCommandAsRootWithOutput(tb testing.TB, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, command string, args ...string) (string, error) {
	tb.Helper()

	return ExecCommandWithOutput(tb, ctx, sbx, envdClient, nil, "root", command, args...)
}

func ExecCommandWithOptions(tb testing.TB, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, cwd *string, user string, command string, args ...string) error {
	tb.Helper()

	_, err := ExecCommandWithOutput(tb, ctx, sbx, envdClient, cwd, user, command, args...)

	return err
}

func ExecCommandWithOutput(tb testing.TB, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, cwd *string, user string, command string, args ...string) (string, error) {
	tb.Helper()

	f := false
	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  command,
			Args: args,
			Cwd:  cwd,
		},
		Stdin: &f,
	})

	setup.SetSandboxHeader(tb, req.Header(), sbx.SandboxID)
	setup.SetUserHeader(tb, req.Header(), user)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := envdClient.ProcessClient.Start(ctx, req)
	if err != nil {
		return "", err
	}
	contextInfo := ""
	if cwd != nil {
		contextInfo += fmt.Sprintf(" (cwd: %s)", *cwd)
	}
	if user != "user" {
		contextInfo += fmt.Sprintf(" (user: %s)", user)
	}
	fmt.Printf("Executing command %s in sandbox %s%s\n", command, sbx.SandboxID, contextInfo)
	defer func() {
		cancel()
		streamErr := stream.Close()
		if streamErr != nil {
			tb.Logf("Error closing stream: %v", streamErr)
		}
	}()

	var output string
	for stream.Receive() {
		select {
		case <-ctx.Done():
			// Context canceled, exit the goroutine
			return "", ctx.Err()
		default:
			msg := stream.Msg()
			tb.Logf("Command [%s] output: %s", command, msg.String())

			// Capture stdout
			if msg.GetEvent().GetData() != nil {
				if stdout := msg.GetEvent().GetData().GetStdout(); stdout != nil {
					output += string(stdout)
				}

				if stderr := msg.GetEvent().GetData().GetStderr(); stderr != nil {
					output += string(stderr)
				}
			}

			if msg.GetEvent().GetEnd() != nil {
				if msg.GetEvent().GetEnd().GetExitCode() != 0 {
					return output, fmt.Errorf("command %s in sandbox %s failed with exit code %d", command, sbx.SandboxID, msg.GetEvent().GetEnd().GetExitCode())
				}
				tb.Logf("Command [%s] completed successfully in sandbox %s", command, sbx.SandboxID)

				return output, nil
			}
		}
	}

	if err := stream.Err(); err != nil {
		return output, fmt.Errorf("failed to execute command %s in sandbox %s: %w", command, sbx.SandboxID, err)
	}

	return output, nil
}
