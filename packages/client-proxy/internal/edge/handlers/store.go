package handlers

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/orchestrators"
	servicediscovery "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type APIStore struct {
	healthStatus api.ClusterNodeStatus

	selfUpdateHandler *func() error
	selfDrainHandler  *func() error

	tracer trace.Tracer
	logger *zap.Logger

	serviceDiscovery  servicediscovery.ServiceDiscoveryAdapter
	orchestratorsPool *orchestrators.Pool
}

func NewStore(ctx context.Context, serviceDiscovery servicediscovery.ServiceDiscoveryAdapter, logger *zap.Logger, tracer trace.Tracer, selfUpdateHandler *func() error, selfDrainHandler *func() error) (*APIStore, error) {
	pool := orchestrators.NewOrchestratorsPool(ctx, logger, serviceDiscovery, tracer)

	return &APIStore{
		serviceDiscovery:  serviceDiscovery,
		orchestratorsPool: pool,

		tracer:       tracer,
		logger:       logger,
		healthStatus: api.Healthy,

		selfDrainHandler:  selfDrainHandler,
		selfUpdateHandler: selfUpdateHandler,
	}, nil
}

func (a *APIStore) SetDraining() {
	a.healthStatus = api.Draining
}

func (a *APIStore) SetUnhealthy() {
	a.healthStatus = api.Unhealthy
}

func (a *APIStore) GracefullyShutdown() {
	a.serviceDiscovery.SetSelfStatus(servicediscovery.StatusDraining)
}

func (a *APIStore) sendAPIStoreError(c *gin.Context, code int, message string) {
	apiErr := api.Error{
		Code:    int32(code),
		Message: message,
	}

	c.Error(fmt.Errorf(message))
	c.JSON(code, apiErr)
}

func parseBody[B any](ctx context.Context, c *gin.Context) (body B, err error) {
	err = c.Bind(&body)
	if err != nil {
		bodyErr := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, bodyErr)
		return body, bodyErr
	}

	return body, nil
}
