package setup

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/tests/integration/internal/testhacks"
)

func GetOrchestratorClient(tb testing.TB, ctx context.Context) orchestrator.SandboxServiceClient {
	tb.Helper()

	conn, err := grpc.NewClient(OrchestratorHost,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(testhacks.GRPCUnaryInterceptor(tb)),
		grpc.WithStreamInterceptor(testhacks.GRPCStreamInterceptor(tb)),
	)
	if err != nil {
		tb.Fatal(fmt.Errorf("failed to establish GRPC connection: %w", err))

		return nil
	}

	go func() {
		<-ctx.Done()
		err = conn.Close()
		assert.NoError(tb, err)
	}()

	return orchestrator.NewSandboxServiceClient(conn)
}
