package setup

import (
	"fmt"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func GetOrchestratorClient(tb testing.TB) orchestrator.SandboxServiceClient {
	tb.Helper()

	conn, err := grpc.NewClient(OrchestratorHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		tb.Fatal(fmt.Errorf("failed to establish GRPC connection: %w", err))

		return nil
	}

	tb.Cleanup(func() {
		err := conn.Close()
		if err != nil {
			tb.Logf("Error closing connection: %v", err)
		}
	})

	return orchestrator.NewSandboxServiceClient(conn)
}
