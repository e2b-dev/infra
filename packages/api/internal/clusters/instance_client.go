package clusters

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type instanceAuthorization struct {
	secret            string
	serviceInstanceID string
	tls               bool
}

// GetRequestMetadata is used for edge gRPC proxy that is secured with auth header and additional using service instance id header for routing
func (a instanceAuthorization) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{
		consts.EdgeRpcProxyAuthHeader:              a.secret,
		consts.EdgeRpcProxyServiceInstanceIDHeader: a.serviceInstanceID,
	}, nil
}

func (a instanceAuthorization) RequireTransportSecurity() bool {
	return a.tls
}

type GRPCClient struct {
	Info     infogrpc.InfoServiceClient
	Sandbox  orchestratorgrpc.SandboxServiceClient
	Template templatemanagergrpc.TemplateServiceClient

	Connection *grpc.ClientConn
	auth       *instanceAuthorization
}

func (a *GRPCClient) Close() error {
	err := a.Connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}

func createConnection(tel *telemetry.Client, auth *instanceAuthorization, endpoint string, endpointTLS bool) (*GRPCClient, error) {
	grpcOptions := []grpc.DialOption{
		grpc.WithStatsHandler(
			otelgrpc.NewClientHandler(
				otelgrpc.WithTracerProvider(tel.TracerProvider),
				otelgrpc.WithMeterProvider(tel.MeterProvider),
			),
		),
		grpc.WithKeepaliveParams(
			keepalive.ClientParameters{
				Time:                10 * time.Second, // Send ping every 10s
				Timeout:             2 * time.Second,  // Wait 2s for response
				PermitWithoutStream: true,
			},
		),
	}

	if auth != nil {
		grpcOptions = append(grpcOptions, grpc.WithPerRPCCredentials(auth))
	}

	if endpointTLS {
		// (2025-06) AWS ALB with TLS termination is using TLS 1.2 as default so this is why we are not using TLS 1.3+ here
		cred := credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
		grpcOptions = append(grpcOptions, grpc.WithAuthority(endpoint), grpc.WithTransportCredentials(cred))
	} else {
		grpcOptions = append(grpcOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.NewClient(endpoint, grpcOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc client: %w", err)
	}

	return &GRPCClient{
		Info:       infogrpc.NewInfoServiceClient(conn),
		Sandbox:    orchestratorgrpc.NewSandboxServiceClient(conn),
		Template:   templatemanagergrpc.NewTemplateServiceClient(conn),
		Connection: conn,
		auth:       auth,
	}, nil
}
