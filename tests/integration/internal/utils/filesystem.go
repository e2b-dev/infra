package utils

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/filesystem"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	envdapi "github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func UploadFile(tb testing.TB, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, path string, content string) {
	tb.Helper()

	buffer, contentType := CreateTextFile(tb, path, content)

	reqEditors := []envdapi.RequestEditorFn{setup.WithSandbox(sbx.SandboxID)}
	if sbx.EnvdAccessToken != nil {
		reqEditors = append(reqEditors, setup.WithEnvdAccessToken(*(sbx.EnvdAccessToken)))
	}

	writeRes, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envdapi.PostFilesParams{Path: &path, Username: "user"},
		contentType,
		buffer,
		reqEditors...,
	)
	if err != nil {
		tb.Fatal(fmt.Errorf("failed to upload file %s: %w", path, err))
	}

	if writeRes.StatusCode() != http.StatusOK {
		tb.Fatal(fmt.Errorf("failed to upload file %s, status code: %d", path, writeRes.StatusCode()))
	}
}

func CreateTextFile(tb testing.TB, path string, content string) (*bytes.Buffer, string) {
	tb.Helper()

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

func CreateDir(t testing.TB, sbx *api.Sandbox, path string) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client := setup.GetEnvdClient(t, ctx)
	req := connect.NewRequest(&filesystem.MakeDirRequest{
		Path: path,
	})
	setup.SetSandboxHeader(req.Header(), sbx.SandboxID)
	setup.SetUserHeader(req.Header(), "user")
	_, err := client.FilesystemClient.MakeDir(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
}
