package setup

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/envd/filesystem/filesystemconnect"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/process/processconnect"
)

const envdPort = 49983

type EnvdClient struct {
	FilesystemClient filesystemconnect.FilesystemClient
	ProcessClient    processconnect.ProcessClient
}

func GetEnvdClient(tb testing.TB, ctx context.Context) *EnvdClient {
	tb.Helper()

	//host := getHost(envdPort, sandboxId, "localhost.debug")
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

func GetEnvdHost(sandboxID string, clientID string) string {
	return fmt.Sprintf("%d-%s-%s.%s", envdPort, sandboxID, clientID, "localhost.debug")
}
