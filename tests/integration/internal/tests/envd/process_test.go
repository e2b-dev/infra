package envd

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestCommandKillNextApp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, client)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Step 1: Run `npx create-next-app`
	createAppReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd: "npx",
			Args: []string{
				"create-next-app@latest", "nextapp", "--yes",
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

	// Step 2: Run `npm run dev` in background
	cwd := "~/nextapp"
	runDevReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "npm",
			Args: []string{"run", "dev"},
			Cwd:  &cwd,
		},
	})
	setup.SetSandboxHeader(runDevReq.Header(), sbx.SandboxID, sbx.ClientID)
	setup.SetUserHeader(runDevReq.Header(), "user")
	runDevStream, err := envdClient.ProcessClient.Start(ctx, runDevReq)
	require.NoError(t, err)
	defer runDevStream.Close()

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

	// Step 3: Wait for next dev to start and list processes
	time.Sleep(10 * time.Second)

	listReq := connect.NewRequest(&process.ListRequest{})
	setup.SetSandboxHeader(listReq.Header(), sbx.SandboxID, sbx.ClientID)
	setup.SetUserHeader(listReq.Header(), "user")
	listResp, err := envdClient.ProcessClient.List(ctx, listReq)
	require.NoError(t, err)

	assert.Len(t, listResp.Msg.Processes, 1, "Expected one process (next dev) running")

	// Step 4: Kill all processes
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

	// Step 5: Final process list
	finalListResp, err := envdClient.ProcessClient.List(ctx, listReq)
	require.NoError(t, err)

	assert.Len(t, finalListResp.Msg.Processes, 0, "Expected no processes running")
	for _, proc := range finalListResp.Msg.Processes {
		t.Errorf("remaining process: PID=%d CMD=%s", proc.Pid, proc.Config.Cmd)
	}
}
