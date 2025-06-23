package analyticscollector

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/emptypb"
)

var host = strings.TrimSpace(os.Getenv("ANALYTICS_COLLECTOR_HOST"))

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
		systemRoots, err := x509.SystemCertPool()
		if err != nil {
			errMsg := fmt.Errorf("failed to read system root certificate pool: %w", err)

			return nil, errMsg
		}

		cred := credentials.NewTLS(&tls.Config{
			RootCAs:    systemRoots,
			MinVersion: tls.VersionTLS13,
		})

		conn, err := grpc.NewClient(
			fmt.Sprintf("%s:443", host),
			grpc.WithPerRPCCredentials(&gRPCApiKey{}),
			grpc.WithAuthority(host),
			grpc.WithTransportCredentials(cred),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create GRPC client: %w", err)
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
