package edge

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

	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type clientAuthorization struct {
	secret string
	tls    bool
}

func (a clientAuthorization) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{consts.EdgeRpcAuthHeader: a.secret}, nil
}

func (a clientAuthorization) RequireTransportSecurity() bool {
	return a.tls
}

func createClusterClient(tel *telemetry.Client, auth clientAuthorization, endpoint string, endpointTLS bool) (*grpclient.GRPCClient, error) {
	grpcOptions := []grpc.DialOption{
		grpc.WithPerRPCCredentials(auth),
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

	return &grpclient.GRPCClient{
		Sandbox:    nil,
		Info:       infogrpc.NewInfoServiceClient(conn),
		Template:   templatemanagergrpc.NewTemplateServiceClient(conn),
		Connection: conn,
	}, nil
}
