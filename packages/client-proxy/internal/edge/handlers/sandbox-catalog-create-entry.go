package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1SandboxCatalogCreate(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := parseBody[api.V1SandboxCatalogCreateJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)
		return
	}

	_, span := a.tracer.Start(ctx, "create-sandbox-catalog-entry-handler")
	defer span.End()

	sbxMaxLifetime := time.Duration(body.SandboxMaxLength) * time.Hour
	sbxInfo := &sandboxes.SandboxInfo{
		OrchestratorID: body.OrchestratorID,
		ExecutionID:    body.ExecutionID,

		SandboxMaxLengthInHours: body.SandboxMaxLength,
		SandboxStartedAt:        body.SandboxStartTime,
	}

	err = a.sandboxes.StoreSandbox(ctx, body.SandboxID, sbxInfo, sbxMaxLifetime)
	if err != nil {
		zap.L().Error("Error when storing sandbox in catalog", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when storing sandbox in catalog")
		telemetry.ReportCriticalError(ctx, "error when storing sandbox in catalog", err)
		return
	}

	zap.L().Info("Sandbox successfully stored in catalog", l.WithSandboxID(body.SandboxID))
	c.Status(http.StatusOK)
}
