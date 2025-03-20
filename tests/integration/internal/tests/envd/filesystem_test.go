package envd

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/filesystem"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
)

func TestFileCreate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	sbxTimeout := int32(60)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}
	})

	assert.Equal(t, http.StatusCreated, resp.StatusCode())

	envdClient := setup.GetEnvdClient(t, ctx)
	req := connect.NewRequest(&filesystem.ListDirRequest{
		Path: "/",
	})
	host := setup.GetEnvdHost(resp.JSON201.SandboxID, resp.JSON201.ClientID)
	t.Logf("Host: %s", host)
	req.Header().Set("Host", host)
	req.Header().Set("Authorization", fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte("user:"))))
	folderListResp, err := envdClient.FilesystemClient.ListDir(ctx, req)
	assert.NoError(t, err)

	t.Logf("Folder list response: %v", folderListResp.Msg)
}
