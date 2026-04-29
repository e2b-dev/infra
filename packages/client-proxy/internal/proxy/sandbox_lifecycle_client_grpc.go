package proxy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

type grpcSandboxLifecycleClient struct {
	conn   *grpc.ClientConn
	client proxygrpc.SandboxServiceClient
}

func NewGrpcSandboxLifecycleClient(address string) (SandboxLifecycleClient, error) {
	// Client-proxy uses this gRPC client to trigger sandbox lifecycle calls when needed.
	if strings.TrimSpace(address) == "" {
		return nil, errors.New("api grpc address is required")
	}

	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("create grpc client: %w", err)
	}

	return &grpcSandboxLifecycleClient{
		conn:   conn,
		client: proxygrpc.NewSandboxServiceClient(conn),
	}, nil
}

func (c *grpcSandboxLifecycleClient) Init(ctx context.Context) {
	e2bgrpc.ObserveConnection(ctx, c.conn, "api-lifecycle")
}

func (c *grpcSandboxLifecycleClient) Close(_ context.Context) error {
	return c.conn.Close()
}

func appendProxyTrafficMetadata(ctx context.Context, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) context.Context {
	ctx = metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataSandboxRequestPort, strconv.FormatUint(sandboxPort, 10))

	if trafficAccessToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataTrafficAccessToken, trafficAccessToken)
	}

	if envdAccessToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataEnvdAccessToken, envdAccessToken)
	}

	return ctx
}

func (c *grpcSandboxLifecycleClient) Resume(ctx context.Context, sandboxId string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) (string, error) {
	ctx = appendProxyTrafficMetadata(ctx, sandboxPort, trafficAccessToken, envdAccessToken)

	resp, err := c.client.ResumeSandbox(ctx, &proxygrpc.SandboxResumeRequest{
		SandboxId: sandboxId,
	})
	if err != nil {
		return "", fmt.Errorf("grpc resume: %w", err)
	}

	return strings.TrimSpace(resp.GetOrchestratorIp()), nil
}

func (c *grpcSandboxLifecycleClient) KeepAlive(ctx context.Context, sandboxId string, teamID string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) error {
	ctx = appendProxyTrafficMetadata(ctx, sandboxPort, trafficAccessToken, envdAccessToken)

	_, err := c.client.KeepAliveSandbox(ctx, &proxygrpc.SandboxKeepAliveRequest{
		SandboxId: sandboxId,
		TeamId:    teamID,
	})
	if err != nil {
		return fmt.Errorf("grpc keepalive: %w", err)
	}

	return nil
}
