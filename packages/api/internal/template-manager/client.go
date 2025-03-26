package template_manager

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

var (
	host                = os.Getenv("TEMPLATE_MANAGER_ADDRESS")
	healthCheckInterval = 5 * time.Second
)

type GRPCClient struct {
	Client     template_manager.TemplateServiceClient
	connection e2bgrpc.ClientConnInterface

	lastHealthCheckAt *time.Time
	health            template_manager.HealthState
}

func NewClient(ctx context.Context) (*GRPCClient, error) {
	keepaliveParam := grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                10 * time.Second, // Send ping every 10s
		Timeout:             2 * time.Second,  // Wait 2s for response
		PermitWithoutStream: true,
	})

	conn, err := e2bgrpc.GetConnection(host, false, grpc.WithStatsHandler(otelgrpc.NewClientHandler()), keepaliveParam)
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	client := &GRPCClient{
		Client:            template_manager.NewTemplateServiceClient(conn),
		health:            template_manager.HealthState_Healthy,
		lastHealthCheckAt: nil,
		connection:        conn,
	}

	// periodically check for health status
	go client.healthCheckSync(ctx)

	return client, nil
}

func (a *GRPCClient) healthCheckSync(ctx context.Context) {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reqCtx, reqCtxCancel := context.WithTimeout(ctx, 5*time.Second)
			healthStatus, err := a.Client.HealthStatus(reqCtx, nil)
			reqCtxCancel()

			healthCheckAt := time.Now()
			a.lastHealthCheckAt = &healthCheckAt

			if err != nil {
				zap.L().Error("failed to get health status of template manager", zap.Error(err))

				a.lastHealthCheckAt = &healthCheckAt
				a.health = template_manager.HealthState_Draining
				continue
			}

			zap.L().Debug("template manager health status", zap.String("status", healthStatus.Status.String()))

			a.lastHealthCheckAt = &healthCheckAt
			a.health = healthStatus.Status
		}
	}
}

func (a *GRPCClient) Close() error {
	err := a.connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}

func (a *GRPCClient) IsReadyForBuildPlacement() bool {
	return a.health == template_manager.HealthState_Healthy
}
