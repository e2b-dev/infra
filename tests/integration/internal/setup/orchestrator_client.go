package setup

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func GetOrchestratorClient(tb testing.TB, ctx context.Context) orchestrator.SandboxServiceClient {
	tb.Helper()

	conn, err := e2bgrpc.GetConnection(OrchestratorHost, false, grpc.WithBlock(), grpc.WithTimeout(time.Second))
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
