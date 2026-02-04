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
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, client, utils.WithTimeout(300))

	envdClient := setup.GetEnvdClient(t, ctx)

	// Run `npx create-next-app`
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "npx", "create-next-app@latest", "nextapp", "--yes")
	require.NoError(t, err)

	// Run `npm run dev` in background
	cwd := "~/nextapp"
	runDevReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "/bin/bash",
			Args: []string{"-l", "-c", "npm run dev"},
			Cwd:  &cwd,
		},
	})
	setup.SetSandboxHeader(runDevReq.Header(), sbx.SandboxID)
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
	setup.SetSandboxHeader(listReq.Header(), sbx.SandboxID)
	setup.SetUserHeader(listReq.Header(), "user")
	listResp, err := envdClient.ProcessClient.List(ctx, listReq)
	require.NoError(t, err)

	assert.Len(t, listResp.Msg.GetProcesses(), 1, "Expected one process (next dev) running")

	// Kill all processes
	for _, proc := range listResp.Msg.GetProcesses() {
		t.Logf("killing process PID=%d CMD=%s", proc.GetPid(), proc.GetConfig().GetCmd())
		killPid(t, ctx, envdClient, sbx.SandboxID, proc.GetPid())
	}

	// Final process list
	finalListResp, err := envdClient.ProcessClient.List(ctx, listReq)
	require.NoError(t, err)

	assert.Empty(t, finalListResp.Msg.GetProcesses(), "Expected no processes running")
	for _, proc := range finalListResp.Msg.GetProcesses() {
		t.Errorf("remaining process: PID=%d CMD=%s", proc.GetPid(), proc.GetConfig().GetCmd())
	}
}

func TestCommandKillWithAnd(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, client, utils.WithTimeout(300))

	envdClient := setup.GetEnvdClient(t, ctx)

	// Run `sleep 30 && echo done` in background
	runDevReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "/bin/bash",
			Args: []string{"-l", "-c", "sleep 30 && echo done"},
		},
	})
	setup.SetSandboxHeader(runDevReq.Header(), sbx.SandboxID)
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
	setup.SetSandboxHeader(listReq.Header(), sbx.SandboxID)
	setup.SetUserHeader(listReq.Header(), "user")
	listResp, err := envdClient.ProcessClient.List(ctx, listReq)
	require.NoError(t, err)

	assert.Len(t, listResp.Msg.GetProcesses(), 1, "Expected one process running")

	// Kill all processes
	for _, proc := range listResp.Msg.GetProcesses() {
		t.Logf("killing process PID=%d CMD=%s", proc.GetPid(), proc.GetConfig().GetCmd())
		killPid(t, ctx, envdClient, sbx.SandboxID, proc.GetPid())
	}

	// Final process list
	finalListResp, err := envdClient.ProcessClient.List(ctx, listReq)
	require.NoError(t, err)

	assert.Empty(t, finalListResp.Msg.GetProcesses(), "Expected no processes running")
	for _, proc := range finalListResp.Msg.GetProcesses() {
		t.Errorf("remaining process: PID=%d CMD=%s", proc.GetPid(), proc.GetConfig().GetCmd())
	}
}

// killPid kills a process by its PID and waits for it to terminate.
func killPid(
	t *testing.T,
	ctx context.Context,
	envdClient *setup.EnvdClient,
	sandboxID string,
	pid uint32,
) {
	t.Helper()

	// Connect to the process
	connectReq := connect.NewRequest(&process.ConnectRequest{
		Process: &process.ProcessSelector{
			Selector: &process.ProcessSelector_Pid{
				Pid: pid,
			},
		},
	})
	setup.SetSandboxHeader(connectReq.Header(), sandboxID)
	setup.SetUserHeader(connectReq.Header(), "user")
	connectResp, err := envdClient.ProcessClient.Connect(ctx, connectReq)
	require.NoError(t, err)

	// Send SIGKILL to the process (doesn't await the termination)
	killReq := connect.NewRequest(&process.SendSignalRequest{
		Signal: process.Signal_SIGNAL_SIGKILL,
		Process: &process.ProcessSelector{
			Selector: &process.ProcessSelector_Pid{
				Pid: pid,
			},
		},
	})
	setup.SetSandboxHeader(killReq.Header(), sandboxID)
	setup.SetUserHeader(killReq.Header(), "user")
	_, err = envdClient.ProcessClient.SendSignal(ctx, killReq)
	require.NoError(t, err)

	// Wait for the process to terminate
	for connectResp.Receive() {
		t.Logf("waiting for process kill: %s", connectResp.Msg())
	}
}

func TestWorkdirDeletion(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, client, utils.WithTimeout(120))

	envdClient := setup.GetEnvdClient(t, ctx)

	testDir := "/tmp/test-workdir"

	err := utils.ExecCommand(t, ctx, sbx, envdClient, "/bin/bash", "-c", "mkdir -p "+testDir+" && echo 'success' > "+testDir+"/test.txt")
	require.NoError(t, err, "Should be able to create test directory")

	err = utils.ExecCommand(t, ctx, sbx, envdClient, "/bin/bash", "-c", "rm -rf "+testDir)
	require.NoError(t, err, "Should be able to delete test directory")

	err = utils.ExecCommandWithCwd(t, ctx, sbx, envdClient, &testDir, "/bin/bash", "-c", "pwd")
	require.Error(t, err, "Should fail when trying to use deleted directory as working directory")
}

func TestWorkdirPermissionDenied(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, client, utils.WithTimeout(120))

	envdClient := setup.GetEnvdClient(t, ctx)

	restrictedDir := "/tmp/restricted-workdir"

	err := utils.ExecCommandAsRoot(t, ctx, sbx, envdClient, "/bin/bash", "-c", "mkdir -p "+restrictedDir+" && chmod 700 "+restrictedDir)
	require.NoError(t, err, "Should be able to create restricted directory as root")

	err = utils.ExecCommand(t, ctx, sbx, envdClient, "/bin/bash", "-c", "test -d "+restrictedDir)
	require.NoError(t, err, "Directory should exist")

	err = utils.ExecCommand(t, ctx, sbx, envdClient, "/bin/bash", "-c", "ls "+restrictedDir)
	require.Error(t, err, "Regular user should not have access to restricted directory")

	err = utils.ExecCommandWithCwd(t, ctx, sbx, envdClient, &restrictedDir, "/bin/bash", "-c", "pwd")
	require.Error(t, err, "Should fail when trying to use restricted directory as working directory")
}

func TestStdinCantRead(t *testing.T) {
	t.Parallel()
	client := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, client, utils.WithTimeout(120))

	envdClient := setup.GetEnvdClient(t, t.Context())

	err := utils.ExecCommandAsRoot(t, t.Context(), sbx, envdClient, "/bin/bash", "-c", "read -p 'Enter your name: '")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exit code 1")
}
