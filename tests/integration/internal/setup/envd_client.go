package setup

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/filesystem/filesystemconnect"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/process/processconnect"
)

const envdPort = 49983

type EnvdClient struct {
	HTTPClient       *api.ClientWithResponses
	FilesystemClient filesystemconnect.FilesystemClient
	ProcessClient    processconnect.ProcessClient
}

func GetEnvdClient(tb testing.TB, _ context.Context) *EnvdClient {
	tb.Helper()

	hc := http.Client{
		Timeout: apiTimeout,
	}

	httpC, err := api.NewClientWithResponses(EnvdProxy, api.WithHTTPClient(&hc))
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

func WithSandbox(sandboxID string, clientID string) func(ctx context.Context, req *http.Request) error {
	return func(ctx context.Context, req *http.Request) error {
		SetSandboxHeader(req.Header, sandboxID, clientID)
		req.Host = req.Header.Get("Host")

		return nil
	}
}

func WithAccessToken(accessToken string) func(ctx context.Context, req *http.Request) error {
	return func(ctx context.Context, req *http.Request) error {
		SetAccessTokenHeader(req.Header, accessToken)
		return nil
	}
}

func SetSandboxHeader(header http.Header, sandboxID string, clientID string) {
	domain := extractDomain(EnvdProxy)
	// Construct the host (<port>-<sandbox id>-<old client id>.e2b.app)
	host := fmt.Sprintf("%d-%s-%s.%s", envdPort, sandboxID, clientID, domain)

	header.Set("Host", host)
}

func SetAccessTokenHeader(header http.Header, accessToken string) {
	header.Set("X-Access-Token", accessToken)
}

func SetUserHeader(header http.Header, user string) {
	userString := fmt.Sprintf("user:%s", user)
	userBase64 := base64.StdEncoding.EncodeToString([]byte(userString))
	basic := fmt.Sprintf("Basic %s", userBase64)
	header.Set("Authorization", basic)
}

func extractDomain(input string) string {
	parsedURL, err := url.Parse(input)
	if err != nil || parsedURL.Host == "" {
		panic(fmt.Errorf("invalid URL: %s", input))
	}

	return parsedURL.Hostname()
}
