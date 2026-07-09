package api

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestMaskRequestHostAPIParameter(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()
	ctx := t.Context()

	hostname := "localhost"
	maskedURL := fmt.Sprintf("%s:%s", hostname, pool.MaskRequestHostPortPlaceholder)
	sbxNet := &api.SandboxNetworkConfig{
		MaskRequestHost: &maskedURL,
	}

	// Create sandbox with maskRequestHost set
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithNetwork(sbxNet), utils.WithTimeout(120))

	envdClient := setup.GetEnvdClient(t, ctx)

	port := 8080
	// Single-shot listener that dumps the raw request to a file. Uses the
	// preinstalled python instead of installing netcat via apt-get, which is
	// page-fault-heavy enough to wedge the sandbox under parallel test load.
	outputFile := "/tmp/nc_output.txt"
	listener := fmt.Sprintf(
		`import socket; s=socket.socket(); s.bind(("0.0.0.0",%d)); s.listen(1); c,_=s.accept(); open(%q,"wb").write(c.recv(65536))`,
		port, outputFile)
	go func() {
		_ = utils.ExecCommandAsRoot(t, ctx, sbx, envdClient, "python3", "-c", listener)
	}()

	// Wait for the listener to be up
	time.Sleep(2 * time.Second)

	// Prepare the sandbox URL using helpers
	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	req := utils.NewRequest(sbx, url, port, nil)
	// The request doesn't finish properly and blocks, but the headers are still sent to the netcat
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		resp.Body.Close()
	}()

	// Give netcat listener time to receive and write to file
	time.Sleep(2 * time.Second)

	// Read file contents
	data, err := utils.ExecCommandAsRootWithOutput(t, ctx, sbx, envdClient, "cat", outputFile)
	require.NoError(t, err)

	// Verify the Host header seen by netcat is the one set via maskRequestHost
	t.Logf("Data: %s", data)
	assert.Contains(t, data, fmt.Sprintf("Host: %s:%d", hostname, port))
}

func TestMaskRequestHostIncorrectUrl(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()
	ctx := t.Context()

	// Create sandbox without maskRequestHost
	utils.AcquireSandboxSlot(t)
	sbxNet := &api.SandboxNetworkConfig{
		MaskRequestHost: new("-https://abcd"),
	}
	createSandboxResponse, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Network:    sbxNet,
	}, setup.WithAPIKey())
	require.NoError(t, err)

	require.Equal(t, http.StatusBadRequest, createSandboxResponse.StatusCode())
	require.NotNil(t, createSandboxResponse.JSON400)
}
