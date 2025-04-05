package envd

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	envdapi "github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/filesystem"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
)

func TestListDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)

	createDir(t, "/test-dir")
	createDir(t, "/test-dir/sub-dir-1")
	createDir(t, "/test-dir/sub-dir-2")
	createTextFile(t, "/test-dir/sub-dir/file.txt", "Hello, World!")

	envdClient := setup.GetEnvdClient(t, ctx)

	// List all files in the root directory with depth 1
	req := connect.NewRequest(&filesystem.ListDirRequest{
		Path:  "/",
		Depth: 1,
	})
	setup.SetSandboxHeader(req.Header(), sbx.SandboxID, sbx.ClientID)
	setup.SetUserHeader(req.Header(), "user")
	folderListResp, err := envdClient.FilesystemClient.ListDir(ctx, req)
	assert.NoError(t, err)

	assert.NotEmpty(t, folderListResp.Msg)
	assert.Equal(t, 1, len(folderListResp.Msg.Entries))
	assert.Equal(t, "test-dir", folderListResp.Msg.Entries[0].Name)
	assert.Equal(t, "test-dir/sub-dir-1", folderListResp.Msg.Entries[1].Name)
}

func TestCreateFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)

	envdClient := setup.GetEnvdClient(t, ctx)
	filePath := "test.txt"
	textFile, contentType := createTextFile(t, filePath, "Hello, World!")

	createFileResp, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envdapi.PostFilesParams{
			Path:     &filePath,
			Username: "user",
		},
		contentType,
		textFile,
		setup.WithSandbox(sbx.SandboxID, sbx.ClientID),
	)
	assert.NoError(t, err)

	assert.Equal(t, http.StatusOK, createFileResp.StatusCode())
}

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
	setup.SetSandboxHeader(req.Header(), sbx.SandboxID, sbx.ClientID)
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

func createTextFile(tb testing.TB, path string, content string) (*bytes.Buffer, string) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", path)
	if err != nil {
		tb.Fatal(err)
	}
	_, err = part.Write([]byte(content))
	if err != nil {
		tb.Fatal(err)
	}
	err = writer.Close()
	if err != nil {
		tb.Fatal(err)
	}

	return body, writer.FormDataContentType()
}

func createDir(tb testing.TB, path string) {
	tb.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := setup.GetEnvdClient(tb, ctx)
	req := connect.NewRequest(&filesystem.MakeDirRequest{
		Path: path,
	})
	setup.SetUserHeader(req.Header(), "user")
	_, err := client.FilesystemClient.MakeDir(ctx, req)
	if err != nil {
		tb.Fatal(err)
	}
}
