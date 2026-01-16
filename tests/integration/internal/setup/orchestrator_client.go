package setup

import (
	"context"
	"fmt"
	"os"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func GetOrchestratorClient(tb testing.TB, ctx context.Context) orchestrator.SandboxServiceClient {
	tb.Helper()

	conn, err := grpc.NewClient(OrchestratorHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		tb.Fatal(fmt.Errorf("failed to establish GRPC connection: %w", err))

		return nil
	}

	go func() {
		<-ctx.Done()
		if err := conn.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to close grpc connection: %v\n", err)
		}
	}()

	return orchestrator.NewSandboxServiceClient(conn)
}
