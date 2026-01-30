package setup

import (
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/filesystem/filesystemconnect"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
	"github.com/e2b-dev/infra/tests/integration/internal/envd"
)

type EnvdClient struct {
	HTTPClient       *envd.ClientWithResponses
	FilesystemClient filesystemconnect.FilesystemClient
	ProcessClient    processconnect.ProcessClient
}

func GetEnvdClient(tb testing.TB, _ context.Context) *EnvdClient {
	tb.Helper()

	hc := http.Client{
		Timeout: envdTimeout,
	}

	httpC, err := envd.NewClientWithResponses(EnvdProxy, envd.WithHTTPClient(&hc))
	if err != nil {
		panic(err)
	}

	fileC := filesystemconnect.NewFilesystemClient(&hc, EnvdProxy)
	processC := processconnect.NewProcessClient(&hc, EnvdProxy)

	return &EnvdClient{
		HTTPClient:       httpC,
		FilesystemClient: fileC,
		ProcessClient:    processC,
	}
}

func WithSandbox(sandboxID string) func(context.Context, *http.Request) error {
	return func(_ context.Context, req *http.Request) error {
		SetSandboxHeader(req.Header, sandboxID)
		req.Host = req.Header.Get("Host")

		return nil
	}
}

func WithEnvdAccessToken(accessToken string) func(ctx context.Context, req *http.Request) error {
	return func(_ context.Context, req *http.Request) error {
		SetAccessTokenHeader(req.Header, accessToken)

		return nil
	}
}

func SetSandboxHeader(header http.Header, sandboxID string) {
	err := grpc.SetSandboxHeader(header, EnvdProxy, sandboxID)
	if err != nil {
		panic(err)
	}
}

func SetAccessTokenHeader(header http.Header, accessToken string) {
	header.Set("X-Access-Token", accessToken)
}

func SetUserHeader(header http.Header, user string) {
	grpc.SetUserHeader(header, user)
}
