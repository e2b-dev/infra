package handlers

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/info"
	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type APIStore struct {
	tracer           trace.Tracer
	logger           *zap.Logger
	info             *info.ServiceInfo
	orchestratorPool *e2borchestrators.OrchestratorsPool
	edgePool         *e2borchestrators.EdgePool
	sandboxes        *sandboxes.SandboxesCatalog
}

func NewStore(_ context.Context, logger *zap.Logger, tracer trace.Tracer, info *info.ServiceInfo, orchestratorsPool *e2borchestrators.OrchestratorsPool, edgePool *e2borchestrators.EdgePool, catalog *sandboxes.SandboxesCatalog) (*APIStore, error) {
	return &APIStore{
		orchestratorPool: orchestratorsPool,
		edgePool:         edgePool,

		info:      info,
		tracer:    tracer,
		logger:    logger,
		sandboxes: catalog,
	}, nil
}

func (a *APIStore) SetDraining() {
	a.info.SetStatus(api.Draining)
}

func (a *APIStore) SetUnhealthy() {
	a.info.SetStatus(api.Unhealthy)
}

func (a *APIStore) GracefullyShutdown() {
	a.SetUnhealthy()
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
