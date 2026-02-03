package setup

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

func GetProxyGrpcClient(tb testing.TB, ctx context.Context) proxygrpc.SandboxServiceClient {
	tb.Helper()

	addr, ok := os.LookupEnv("TESTS_API_GRPC_ADDRESS")
	if !ok || strings.TrimSpace(addr) == "" {
		tb.Skip("Skipping gRPC test: TESTS_API_GRPC_ADDRESS is not set")

		return nil
	}

	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		dialCtx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		tb.Fatal(fmt.Errorf("failed to establish proxy gRPC connection to %s: %w", addr, err))

		return nil
	}

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	return proxygrpc.NewSandboxServiceClient(conn)
}
