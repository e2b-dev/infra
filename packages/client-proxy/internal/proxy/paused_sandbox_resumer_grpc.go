package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

type grpcPausedSandboxResumer struct {
	conn      *grpc.ClientConn
	client    proxygrpc.SandboxServiceClient
	apiSecret string
}

func NewGrpcPausedSandboxResumer(address string, apiSecret string, tlsEnabled bool) (PausedSandboxResumer, error) {
	// Client-proxy uses this gRPC client to trigger ResumeSandbox when needed.
	if strings.TrimSpace(address) == "" {
		return nil, errors.New("api grpc address is required")
	}
	if tlsEnabled && strings.TrimSpace(apiSecret) == "" {
		return nil, fmt.Errorf("api secret is required when api grpc tls is enabled")
	}

	var creds credentials.TransportCredentials = insecure.NewCredentials()
	if tlsEnabled {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}

	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(creds),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("create grpc client: %w", err)
	}

	return &grpcPausedSandboxResumer{
		conn:      conn,
		client:    proxygrpc.NewSandboxServiceClient(conn),
		apiSecret: strings.TrimSpace(apiSecret),
	}, nil
}

func (c *grpcPausedSandboxResumer) Init(ctx context.Context) {
	e2bgrpc.ObserveConnection(ctx, c.conn, "api-resumer")
}

func (c *grpcPausedSandboxResumer) Close(_ context.Context) error {
	return c.conn.Close()
}

func (c *grpcPausedSandboxResumer) Resume(ctx context.Context, sandboxId string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) (string, error) {
	ctx = metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataSandboxRequestPort, strconv.FormatUint(sandboxPort, 10))

	if trafficAccessToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataTrafficAccessToken, trafficAccessToken)
	}

	if envdAccessToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataEnvdAccessToken, envdAccessToken)
	}
	if c.apiSecret != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataClientProxyAuthToken, c.apiSecret)
	}

	resp, err := c.client.ResumeSandbox(ctx, &proxygrpc.SandboxResumeRequest{
		SandboxId: sandboxId,
	})
	if err != nil {
		return "", fmt.Errorf("grpc resume: %w", err)
	}

	return strings.TrimSpace(resp.GetOrchestratorIp()), nil
}
