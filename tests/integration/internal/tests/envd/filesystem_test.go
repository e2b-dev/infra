package envd

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	envdapi "github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/filesystem"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
)

func TestListDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sbx := createSandbox(t, setup.WithAPIKey())

	envdClient := setup.GetEnvdClient(t, ctx)
	req := connect.NewRequest(&filesystem.ListDirRequest{
		Path: "/",
	})
	setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID, sbx.JSON201.ClientID)
	setup.SetUserHeader(req.Header(), "user")
	folderListResp, err := envdClient.FilesystemClient.ListDir(ctx, req)
	assert.NoError(t, err)

	assert.NotEmpty(t, folderListResp.Msg)
}

func TestCreateFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sbx := createSandbox(t, setup.WithAPIKey())

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
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)
	assert.NoError(t, err)

	assert.Equal(t, http.StatusOK, createFileResp.StatusCode())
}

func TestFilePermissions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sbx := createSandbox(t, setup.WithAPIKey())

	envdClient := setup.GetEnvdClient(t, ctx)
	stream, err := envdClient.ProcessClient.Start(
		ctx,
		connect.NewRequest(&process.StartRequest{
			Process: &process.ProcessConfig{
				Cmd:  "ls",
				Args: []string{"-la", "/home/user"},
			},
		}),
	)

	assert.NoError(t, err)

	defer stream.Close()

	for stream.Receive() {
		msg := stream.Msg()
		t.Log(msg)
	}

	assert.NoError(t, stream.Err())
}

func createSandbox(t *testing.T, reqEditors ...api.RequestEditorFn) *api.PostSandboxesResponse {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	sbxTimeout := int32(30)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
	}, reqEditors...)
	assert.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}
	})

	assert.Equal(t, http.StatusCreated, resp.StatusCode())

	return resp
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
