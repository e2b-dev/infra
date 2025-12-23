package pool

import (
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	e2bgrpcorchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type instanceGRPCClient struct {
	info       e2bgrpcorchestratorinfo.InfoServiceClient
	connection *grpc.ClientConn
}

func newClient(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, host string) (*instanceGRPCClient, error) {
	conn, err := grpc.NewClient(host,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(
			otelgrpc.NewClientHandler(
				otelgrpc.WithTracerProvider(tracerProvider),
				otelgrpc.WithMeterProvider(meterProvider),
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create GRPC client: %w", err)
	}

	return &instanceGRPCClient{
		info:       e2bgrpcorchestratorinfo.NewInfoServiceClient(conn),
		connection: conn,
	}, nil
}

func (a *instanceGRPCClient) close() error {
	err := a.connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}
