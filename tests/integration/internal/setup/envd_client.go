package setup

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/envd/filesystem/filesystemconnect"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/process/processconnect"

	"connectrpc.com/connect"
)

const envdPort = 49983

type EnvdClient struct {
	FilesystemClient filesystemconnect.FilesystemClient
	ProcessClient    processconnect.ProcessClient
}

func GetEnvdClient(tb testing.TB, ctx context.Context) *EnvdClient {
	tb.Helper()

	hc := http.Client{
		Timeout: apiTimeout,
	}

	fileC := filesystemconnect.NewFilesystemClient(&hc, EnvdProxy)
	processC := processconnect.NewProcessClient(&hc, EnvdProxy)

	return &EnvdClient{
		FilesystemClient: fileC,
		ProcessClient:    processC,
	}
}

func WithSandbox[T any](req *connect.Request[T], sandboxID string, clientID string) {
	domain := extractDomain(EnvdProxy)
	host := fmt.Sprintf("%d-%s-%s.%s", envdPort, sandboxID, clientID, domain)

	req.Header().Set("Host", host)
}

func WithUser[T any](req *connect.Request[T], user string) {
	userString := fmt.Sprintf("user:%s", user)
	userBase64 := base64.StdEncoding.EncodeToString([]byte(userString))
	basic := fmt.Sprintf("Basic %s", userBase64)
	req.Header().Set("Authorization", basic)
}

func extractDomain(input string) string {
	parsedURL, err := url.Parse(input)
	if err != nil || parsedURL.Host == "" {
		panic(fmt.Errorf("invalid URL: %s", input))
	}

	return parsedURL.Hostname()
}
