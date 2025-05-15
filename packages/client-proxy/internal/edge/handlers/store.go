package handlers

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/info"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type APIStore struct {
	healthStatus api.ClusterNodeStatus

	selfUpdateHandler *func() error
	selfDrainHandler  *func() error

	tracer trace.Tracer
	logger *zap.Logger
	info   *info.ServiceInfo

	//serviceDiscovery  servicediscovery.ServiceDiscoveryAdapter
	//orchestratorsPool *orchestrators.Pool
}

func NewStore(ctx context.Context, logger *zap.Logger, tracer trace.Tracer, info *info.ServiceInfo, selfUpdateHandler *func() error, selfDrainHandler *func() error) (*APIStore, error) {
	//pool := orchestrators.NewOrchestratorsPool(ctx, logger, serviceDiscovery, tracer)

	return &APIStore{
		//serviceDiscovery:  serviceDiscovery,
		//orchestratorsPool: pool,

		info:         info,
		tracer:       tracer,
		logger:       logger,
		healthStatus: api.Healthy,

		selfDrainHandler:  selfDrainHandler,
		selfUpdateHandler: selfUpdateHandler,
	}, nil
}

func (a *APIStore) SetDraining() {
	a.info.SetStatus(api.Draining)
}

func (a *APIStore) SetUnhealthy() {
	a.info.SetStatus(api.Unhealthy)
}

func (a *APIStore) GracefullyShutdown() {
	// todo
	//a.serviceDiscovery.SetSelfStatus(servicediscovery.StatusDraining)
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
