package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1SandboxCatalogCreate(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := parseBody[api.V1SandboxCatalogCreateJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportError(ctx, "error when parsing request", err)

		return
	}

	ctx, span := tracer.Start(ctx, "create-sandbox-catalog-entry-handler")
	defer span.End()

	o, ok := a.orchestratorPool.GetOrchestrator(body.OrchestratorID)
	if !ok {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Orchestrator not found")
		telemetry.ReportError(ctx, "orchestrator not found", nil)

		return
	}

	sbxMaxLifetime := time.Duration(body.SandboxMaxLength) * time.Hour
	sbxInfo := &catalog.SandboxInfo{
		OrchestratorID: body.OrchestratorID,
		OrchestratorIP: o.GetInfo().IP,
		ExecutionID:    body.ExecutionID,

		SandboxMaxLengthInHours: body.SandboxMaxLength,
		SandboxStartedAt:        body.SandboxStartTime,
	}

	err = a.sandboxes.StoreSandbox(ctx, body.SandboxID, sbxInfo, sbxMaxLifetime)
	if err != nil {
		logger.L().Error(ctx, "Error when storing sandbox in catalog", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when storing sandbox in catalog")
		telemetry.ReportCriticalError(ctx, "error when storing sandbox in catalog", err)

		return
	}

	logger.L().Info(ctx, "Sandbox successfully stored in catalog", l.WithSandboxID(body.SandboxID))
	c.Status(http.StatusOK)
}
