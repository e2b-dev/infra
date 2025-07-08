package utils

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	envdapi "github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func UploadFile(tb testing.TB, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, path string, content []byte) error {
	buffer, contentType := CreateTextFile(tb, path, string(content))
	writeRes, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envdapi.PostFilesParams{Path: &path, Username: "user"},
		contentType,
		buffer,
		setup.WithSandbox(sbx.SandboxID),
	)
	if err != nil {
		return fmt.Errorf("failed to upload file %s: %w", path, err)
	}

	if writeRes.StatusCode() != http.StatusOK {
		return fmt.Errorf("failed to upload file %s, status code: %d", path, writeRes.StatusCode())
	}

	return nil
}

func CreateTextFile(tb testing.TB, path string, content string) (*bytes.Buffer, string) {
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
