package setup

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func GetOrchestratorClient(tb testing.TB, ctx context.Context) orchestrator.SandboxServiceClient {
	tb.Helper()

	if OrchestratorHost == "" {
		tb.Skip("TESTS_ORCHESTRATOR_HOST is not set (run 'make connect-orchestrator' for SSH tunnel)")
	}

	conn, err := grpc.NewClient(OrchestratorHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		tb.Fatal(fmt.Errorf("failed to establish GRPC connection: %w", err))

		return nil
	}

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	return orchestrator.NewSandboxServiceClient(conn)
}
