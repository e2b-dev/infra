package envd

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestCommandKillNextApp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanupWithTimeout(t, client, 300)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Run `npx create-next-app`
	createAppReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd: "/bin/bash",
			Args: []string{
				"-l", "-c", "npx create-next-app@latest nextapp --yes",
			},
		},
	})
	setup.SetSandboxHeader(createAppReq.Header(), sbx.SandboxID, sbx.ClientID)
	setup.SetUserHeader(createAppReq.Header(), "user")
	createAppStream, err := envdClient.ProcessClient.Start(ctx, createAppReq)
	require.NoError(t, err)
	defer createAppStream.Close()

	for createAppStream.Receive() {
		t.Log("create:", createAppStream.Msg())
	}
	require.NoError(t, createAppStream.Err())

	// Run `npm run dev` in background
	cwd := "~/nextapp"
	runDevReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "/bin/bash",
			Args: []string{"-l", "-c", "npm run dev"},
			Cwd:  &cwd,
		},
	})
	setup.SetSandboxHeader(runDevReq.Header(), sbx.SandboxID, sbx.ClientID)
	setup.SetUserHeader(runDevReq.Header(), "user")
	serverCtx, serverCancel := context.WithCancel(ctx)
	runDevStream, err := envdClient.ProcessClient.Start(serverCtx, runDevReq)
	require.NoError(t, err)
	defer func() {
		serverCancel()
		runDevStream.Close()
	}()

	// Read dev output
	receiveDone := make(chan error, 1)
	go func() {
		defer close(receiveDone)
		for runDevStream.Receive() {
			t.Log("dev:", runDevStream.Msg())
		}
		receiveDone <- runDevStream.Err()
	}()

	defer func() {
		select {
		case <-ctx.Done():
			t.Logf("Context done while receiving dev logs: %v", ctx.Err())
			_ = runDevStream.Close()
		case err := <-receiveDone:
			require.NoError(t, err, "streaming ended with error")
		}
	}()

	// Wait for the next dev to start and list processes
	time.Sleep(10 * time.Second)

	listReq := connect.NewRequest(&process.ListRequest{})
	setup.SetSandboxHeader(listReq.Header(), sbx.SandboxID, sbx.ClientID)
	setup.SetUserHeader(listReq.Header(), "user")
	listResp, err := envdClient.ProcessClient.List(ctx, listReq)
	require.NoError(t, err)

	assert.Len(t, listResp.Msg.Processes, 1, "Expected one process (next dev) running")

	// Kill all processes
	for _, proc := range listResp.Msg.Processes {
		t.Logf("killing process PID=%d CMD=%s", proc.Pid, proc.Config.Cmd)
		killReq := connect.NewRequest(&process.SendSignalRequest{
			Signal: process.Signal_SIGNAL_SIGKILL,
			Process: &process.ProcessSelector{
				Selector: &process.ProcessSelector_Pid{
					Pid: proc.Pid,
				},
			},
		})
		setup.SetSandboxHeader(killReq.Header(), sbx.SandboxID, sbx.ClientID)
		setup.SetUserHeader(killReq.Header(), "user")
		_, err := envdClient.ProcessClient.SendSignal(ctx, killReq)
		assert.NoError(t, err)
	}

	// Final process list
	finalListResp, err := envdClient.ProcessClient.List(ctx, listReq)
	require.NoError(t, err)

	assert.Len(t, finalListResp.Msg.Processes, 0, "Expected no processes running")
	for _, proc := range finalListResp.Msg.Processes {
		t.Errorf("remaining process: PID=%d CMD=%s", proc.Pid, proc.Config.Cmd)
	}
}

func TestCommandKillWithAnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanupWithTimeout(t, client, 300)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Run `sleep 30 && echo done` in background
	runDevReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "/bin/bash",
			Args: []string{"-l", "-c", "sleep 30 && echo done"},
		},
	})
	setup.SetSandboxHeader(runDevReq.Header(), sbx.SandboxID, sbx.ClientID)
	setup.SetUserHeader(runDevReq.Header(), "user")
	serverCtx, serverCancel := context.WithCancel(ctx)
	runDevStream, err := envdClient.ProcessClient.Start(serverCtx, runDevReq)
	require.NoError(t, err)
	defer func() {
		serverCancel()
		streamErr := runDevStream.Close()
		if streamErr != nil {
			t.Logf("Error closing stream: %v", streamErr)
		}
	}()

	// Read dev output
	receiveDone := make(chan error, 1)
	go func() {
		defer close(receiveDone)
		for runDevStream.Receive() {
			t.Log("cmd:", runDevStream.Msg())
		}
		receiveDone <- runDevStream.Err()
	}()

	defer func() {
		select {
		case <-ctx.Done():
			t.Logf("Context done while receiving cmd logs: %v", ctx.Err())
			_ = runDevStream.Close()
		case err := <-receiveDone:
			require.NoError(t, err, "streaming ended with error")
		}
	}()

	// Step 2: Wait for the command to start
	time.Sleep(5 * time.Second)

	listReq := connect.NewRequest(&process.ListRequest{})
	setup.SetSandboxHeader(listReq.Header(), sbx.SandboxID, sbx.ClientID)
	setup.SetUserHeader(listReq.Header(), "user")
	listResp, err := envdClient.ProcessClient.List(ctx, listReq)
	require.NoError(t, err)

	assert.Len(t, listResp.Msg.Processes, 1, "Expected one process running")

	// Kill all processes
	for _, proc := range listResp.Msg.Processes {
		t.Logf("killing process PID=%d CMD=%s", proc.Pid, proc.Config.Cmd)
		killReq := connect.NewRequest(&process.SendSignalRequest{
			Signal: process.Signal_SIGNAL_SIGKILL,
			Process: &process.ProcessSelector{
				Selector: &process.ProcessSelector_Pid{
					Pid: proc.Pid,
				},
			},
		})
		setup.SetSandboxHeader(killReq.Header(), sbx.SandboxID, sbx.ClientID)
		setup.SetUserHeader(killReq.Header(), "user")
		_, err := envdClient.ProcessClient.SendSignal(ctx, killReq)
		assert.NoError(t, err)
	}

	// Final process list
	finalListResp, err := envdClient.ProcessClient.List(ctx, listReq)
	require.NoError(t, err)

	assert.Len(t, finalListResp.Msg.Processes, 0, "Expected no processes running")
	for _, proc := range finalListResp.Msg.Processes {
		t.Errorf("remaining process: PID=%d CMD=%s", proc.Pid, proc.Config.Cmd)
	}
}
