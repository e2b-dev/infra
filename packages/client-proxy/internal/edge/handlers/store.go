package handlers

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/info"
	logger_provider "github.com/e2b-dev/infra/packages/proxy/internal/edge/logger-provider"
	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	e2borchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type APIStore struct {
	tracer            trace.Tracer
	logger            *zap.Logger
	info              *info.ServiceInfo
	orchestratorPool  *e2borchestrators.OrchestratorsPool
	edgePool          *e2borchestrators.EdgePool
	sandboxes         *sandboxes.SandboxesCatalog
	queryLogsProvider logger_provider.LogsQueryProvider
}

type APIUserFacingError struct {
	internalError error

	prettyErrorMessage string
	prettyErrorCode    int
}

func NewStore(_ context.Context, logger *zap.Logger, tracer trace.Tracer, info *info.ServiceInfo, orchestratorsPool *e2borchestrators.OrchestratorsPool, edgePool *e2borchestrators.EdgePool, catalog *sandboxes.SandboxesCatalog) (*APIStore, error) {
	queryLogsProvider, err := logger_provider.GetLogsQueryProvider()
	if err != nil {
		return nil, fmt.Errorf("error when getting logs query provider: %w", err)
	}

	return &APIStore{
		orchestratorPool:  orchestratorsPool,
		edgePool:          edgePool,
		queryLogsProvider: queryLogsProvider,

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
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)
		return body, bodyErr
	}

	return body, nil
}

func (a *APIStore) getOrchestratorNode(orchestratorId string) (*e2borchestrators.OrchestratorNode, *APIUserFacingError) {
	orchestrator, ok := a.orchestratorPool.GetOrchestrator(orchestratorId)
	if !ok {
		return nil, &APIUserFacingError{
			internalError:      fmt.Errorf("orchestrator not found: %s", orchestratorId),
			prettyErrorCode:    http.StatusBadRequest,
			prettyErrorMessage: "Orchestrator not found",
		}
	}

	if orchestrator.Status != e2borchestrators.OrchestratorStatusHealthy {
		return nil, &APIUserFacingError{
			internalError:      fmt.Errorf("orchestrator is not ready for build placement"),
			prettyErrorCode:    http.StatusBadRequest,
			prettyErrorMessage: "Orchestrator is not ready for build placement",
		}
	}

	return orchestrator, nil
}

func (a *APIStore) getTemplateManagerNode(orchestratorId string) (*e2borchestrators.OrchestratorNode, *APIUserFacingError) {
	orchestrator, err := a.getOrchestratorNode(orchestratorId)
	if err != nil {
		return nil, err
	}

	if !slices.Contains(orchestrator.Roles, e2borchestrator.ServiceInfoRole_TemplateManager) {
		return nil, &APIUserFacingError{
			internalError:      fmt.Errorf("orchestrator does not support template builds: %w", err),
			prettyErrorCode:    http.StatusBadRequest,
			prettyErrorMessage: "Orchestrator does not support template builds",
		}
	}

	return orchestrator, nil
}
