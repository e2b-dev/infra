package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/info"
	logger_provider "github.com/e2b-dev/infra/packages/proxy/internal/edge/logger-provider"
	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type APIStore struct {
	tracer            trace.Tracer
	logger            *zap.Logger
	info              *info.ServiceInfo
	orchestratorPool  *e2borchestrators.OrchestratorsPool
	edgePool          *e2borchestrators.EdgePool
	sandboxes         sandboxes.SandboxesCatalog
	queryLogsProvider logger_provider.LogsQueryProvider
}

type APIUserFacingError struct {
	internalError error

	prettyErrorMessage string
	prettyErrorCode    int
}

func NewStore(ctx context.Context, logger *zap.Logger, tracer trace.Tracer, info *info.ServiceInfo, orchestratorsPool *e2borchestrators.OrchestratorsPool, edgePool *e2borchestrators.EdgePool, catalog sandboxes.SandboxesCatalog) (*APIStore, error) {
	queryLogsProvider, err := logger_provider.GetLogsQueryProvider()
	if err != nil {
		return nil, fmt.Errorf("error when getting logs query provider: %w", err)
	}

	store := &APIStore{
		orchestratorPool:  orchestratorsPool,
		edgePool:          edgePool,
		queryLogsProvider: queryLogsProvider,

		info:      info,
		tracer:    tracer,
		logger:    logger,
		sandboxes: catalog,
	}

	// Wait till there's at least one orchestrator available
	// we don't want to source API until we are sure service discovery and pool is ready to use
	go func() {
		if env.IsDebug() {
			zap.L().Info("Skipping orchestrator readiness check in debug mode")
			store.info.SetStatus(api.Healthy)
			return
		}

		ticker := time.NewTicker(100 * time.Millisecond)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				list := orchestratorsPool.GetOrchestrators()
				if len(list) > 0 {
					zap.L().Info("Marking API as healthy, at least one orchestrator is available")
					store.info.SetStatus(api.Healthy)
					return
				}
			}
		}
	}()

	return store, nil
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

	c.Error(errors.New(message))
	c.JSON(code, apiErr)
}

func parseBody[B any](ctx context.Context, c *gin.Context) (body B, err error) {
	err = c.Bind(&body)
	if err != nil {
		bodyErr := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)
		return body, bodyErr
	}

	return body, nil
}
