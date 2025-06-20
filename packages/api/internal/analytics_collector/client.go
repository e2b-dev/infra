package analyticscollector

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
)

var host = os.Getenv("ANALYTICS_COLLECTOR_HOST")

type Analytics struct {
	client     AnalyticsCollectorClient
	connection *grpc.ClientConn
}

func NewAnalytics() (*Analytics, error) {
	var client AnalyticsCollectorClient
	var connection *grpc.ClientConn

	if host == "" {
		zap.L().Warn("Running dummy implementation of analytics collector client, no host provided")
	} else {
		conn, err := e2bgrpc.GetConnection(host, true, grpc.WithPerRPCCredentials(&gRPCApiKey{}))
		if err != nil {
			return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
		}

		connection = conn
		client = NewAnalyticsCollectorClient(connection)
	}

	return &Analytics{client: client, connection: connection}, nil
}

func (a *Analytics) Close() error {
	if a.connection == nil {
		return nil
	}

	err := a.connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}

func (a *Analytics) InstanceStarted(ctx context.Context, in *InstanceStartedEvent, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	if a.client == nil {
		return nil, nil
	}

	return a.client.InstanceStarted(ctx, in, opts...)
}

func (a *Analytics) RunningInstances(ctx context.Context, in *RunningInstancesEvent, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	if a.client == nil {
		return nil, nil
	}

	return a.client.RunningInstances(ctx, in, opts...)
}

func (a *Analytics) InstanceStopped(ctx context.Context, in *InstanceStoppedEvent, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	if a.client == nil {
		return nil, nil
	}

	return a.client.InstanceStopped(ctx, in, opts...)
}
