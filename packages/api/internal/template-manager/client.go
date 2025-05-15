package template_manager

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/emptypb"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

var (
	host                = os.Getenv("TEMPLATE_MANAGER_ADDRESS")
	healthCheckInterval = 5 * time.Second
)

type GRPCClient struct {
	TemplateClient templatemanager.TemplateServiceClient
	InfoClient     orchestratorinfo.InfoServiceClient

	connection e2bgrpc.ClientConnInterface

	lastHealthCheckAt *time.Time
	health            orchestratorinfo.ServiceInfoStatus
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
		TemplateClient: templatemanager.NewTemplateServiceClient(conn),
		InfoClient:     orchestratorinfo.NewInfoServiceClient(conn),

		health:            orchestratorinfo.ServiceInfoStatus_OrchestratorHealthy,
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
			reqCheckAt := time.Now()

			infoStatus := a.health
			infoRes, infoErr := a.InfoClient.ServiceInfo(reqCtx, &emptypb.Empty{})
			if infoErr != nil {
				// try  use deprecated health check that is there because back compatibility
				healthStatus, healthStatusErr := a.TemplateClient.HealthStatus(reqCtx, nil)
				reqCtxCancel()

				if healthStatusErr != nil {
					zap.L().Error("failed to get health status of template manager", zap.Error(healthStatusErr))
					infoStatus = orchestratorinfo.ServiceInfoStatus_OrchestratorDraining
				}

				switch healthStatus.Status {
				case templatemanager.HealthState_Healthy:
					infoStatus = orchestratorinfo.ServiceInfoStatus_OrchestratorHealthy
				case templatemanager.HealthState_Draining:
					infoStatus = orchestratorinfo.ServiceInfoStatus_OrchestratorDraining
				}
			} else {
				infoStatus = infoRes.ServiceStatus
			}

			reqCtxCancel()

			zap.L().Debug("template manager health status", zap.String("status", infoRes.ServiceStatus.String()))

			a.health = infoStatus
			a.lastHealthCheckAt = &reqCheckAt
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
	return a.health == orchestratorinfo.ServiceInfoStatus_OrchestratorHealthy
}
