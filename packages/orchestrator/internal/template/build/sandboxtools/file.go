package sandboxtools

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const fileCopyTimeout = 10 * time.Minute

var client = http.Client{
	Timeout: fileCopyTimeout,
}

func CopyFile(
	ctx context.Context,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	user string,
	sourcePath string,
	targetPath string,
) error {
	ctx, span := tracer.Start(ctx, "copy-file")
	defer span.End()

	file, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer file.Close()

	// Pipe to stream data
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	errChan := make(chan error, 1)

	go func() {
		defer writer.Close()

		part, err := writer.CreateFormFile("file", filepath.Base(sourcePath))
		if err != nil {
			pw.CloseWithError(fmt.Errorf("failed to create form file: %w", err))
			errChan <- err
			return
		}

		if _, err := io.Copy(part, file); err != nil {
			pw.CloseWithError(fmt.Errorf("failed to copy file: %w", err))
			errChan <- err
			return
		}

		pw.Close()
		errChan <- nil
	}()

	// Prepare query parameters
	proxyHost := fmt.Sprintf("http://localhost%s", proxy.GetAddr())
	params := url.Values{}
	params.Add("path", targetPath)
	params.Add("username", user)

	telemetry.ReportEvent(ctx, "copy_file",
		attribute.String("source.path", sourcePath),
		attribute.String("target.path", targetPath),
		attribute.String("proxy.host", proxyHost),
		attribute.String("sandbox.id", sandboxID),
	)
	uploadURL := fmt.Sprintf("%s/files?%s", proxyHost, params.Encode())

	// Create HTTP request with streaming body
	req, err := http.NewRequest("POST", uploadURL, pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	err = grpc.SetSandboxHeader(req.Header, proxyHost, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to set request header: %w", err)
	}
	req.Host = req.Header.Get("Host")

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	telemetry.ReportEvent(ctx, "file_upload",
		attribute.Int("response.code", resp.StatusCode),
		attribute.String("response.body", string(body)),
	)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to upload file (%d): %s", resp.StatusCode, string(body))
	}

	if goroutineErr := <-errChan; goroutineErr != nil {
		return fmt.Errorf("file upload failed: %w", goroutineErr)
	}

	return nil
}
