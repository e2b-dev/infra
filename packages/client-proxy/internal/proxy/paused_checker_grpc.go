package proxy

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

type grpcPausedSandboxChecker struct {
	conn   *grpc.ClientConn
	client proxygrpc.ProxySandboxServiceClient
}

func NewGrpcPausedSandboxChecker(address string) (PausedSandboxChecker, error) {
	if strings.TrimSpace(address) == "" {
		return nil, nil
	}

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("create grpc client: %w", err)
	}

	return &grpcPausedSandboxChecker{
		conn:   conn,
		client: proxygrpc.NewProxySandboxServiceClient(conn),
	}, nil
}

func (c *grpcPausedSandboxChecker) Close(_ context.Context) error {
	return c.conn.Close()
}

func (c *grpcPausedSandboxChecker) PausedInfo(ctx context.Context, sandboxId string) (PausedInfo, error) {
	resp, err := c.client.GetPausedInfo(ctx, &proxygrpc.SandboxPausedInfoRequest{
		SandboxId: sandboxId,
	})
	if err != nil {
		return PausedInfo{}, fmt.Errorf("grpc paused info: %w", err)
	}

	return PausedInfo{
		Paused:           resp.GetPaused(),
		AutoResumePolicy: resp.GetAutoResumePolicy(),
	}, nil
}

func (c *grpcPausedSandboxChecker) Resume(ctx context.Context, sandboxId string, timeoutSeconds int32) error {
	_, err := c.client.ResumeSandbox(ctx, &proxygrpc.SandboxResumeRequest{
		SandboxId:      sandboxId,
		TimeoutSeconds: timeoutSeconds,
	})
	if err != nil {
		return fmt.Errorf("grpc resume: %w", err)
	}

	return nil
}
