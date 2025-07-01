package template_manager

import (
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

var templateManagerHost = os.Getenv("TEMPLATE_MANAGER_HOST")

func createClient(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) (*grpclient.GRPCClient, error) {
	conn, err := grpc.NewClient(templateManagerHost,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(
			otelgrpc.NewClientHandler(
				otelgrpc.WithTracerProvider(tracerProvider),
				otelgrpc.WithMeterProvider(meterProvider),
			),
		),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second, // Send ping every 10s
			Timeout:             2 * time.Second,  // Wait 2s for response
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, err
	}

	client := &grpclient.GRPCClient{
		Sandbox:    nil,
		Info:       infogrpc.NewInfoServiceClient(conn),
		Template:   templatemanagergrpc.NewTemplateServiceClient(conn),
		Connection: conn,
	}

	return client, nil
}
