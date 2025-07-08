package envd

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestFilePermissions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)

	envdClient := setup.GetEnvdClient(t, ctx)
	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "ls",
			Args: []string{"-la", "/home/user"},
		},
	})
	setup.SetSandboxHeader(req.Header(), sbx.SandboxID)
	setup.SetUserHeader(req.Header(), "user")
	stream, err := envdClient.ProcessClient.Start(
		ctx,
		req,
	)

	assert.NoError(t, err)

	defer stream.Close()

	out := []string{}

	for stream.Receive() {
		msg := stream.Msg()
		out = append(out, msg.String())
	}

	// in the output, we should see the files .bashrc and .profile, and they should have the correct permissions
	for _, line := range out {
		if strings.Contains(line, ".bashrc") || strings.Contains(line, ".profile") {
			assert.Contains(t, line, "-rw-r--r--")
			assert.Contains(t, line, "user user")
		}
	}
}
