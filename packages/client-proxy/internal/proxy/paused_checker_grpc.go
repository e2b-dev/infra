package proxy

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

type grpcPausedSandboxResumer struct {
	conn   *grpc.ClientConn
	client proxygrpc.SandboxServiceClient
}

func NewGrpcPausedSandboxResumer(address string) (PausedSandboxResumer, error) {
	// Client-proxy uses this gRPC client to trigger ResumeSandbox when needed.
	if strings.TrimSpace(address) == "" {
		return nil, fmt.Errorf("api grpc address is required")
	}

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("create grpc client: %w", err)
	}

	return &grpcPausedSandboxResumer{
		conn:   conn,
		client: proxygrpc.NewSandboxServiceClient(conn),
	}, nil
}

func (c *grpcPausedSandboxResumer) Close(_ context.Context) error {
	return c.conn.Close()
}

func (c *grpcPausedSandboxResumer) Resume(ctx context.Context, sandboxId string, timeoutSeconds int32) error {
	_, err := c.client.ResumeSandbox(ctx, &proxygrpc.SandboxResumeRequest{
		SandboxId:      sandboxId,
		TimeoutSeconds: timeoutSeconds,
	})
	if err != nil {
		return fmt.Errorf("grpc resume: %w", err)
	}

	return nil
}
