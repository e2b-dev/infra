package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1SandboxCatalogDelete(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := parseBody[api.V1SandboxCatalogDeleteJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)
		return
	}

	_, span := a.tracer.Start(ctx, "delete-sandbox-catalog-entry-handler")
	defer span.End()

	err = a.sandboxes.DeleteSandbox(ctx, body.SandboxID, body.ExecutionID)
	if err != nil {
		zap.L().Error("Error when deleting sandbox from catalog", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting sandbox from catalog")
		telemetry.ReportCriticalError(ctx, "error when deleting sandbox from catalog", err)
		return
	}

	zap.L().Info("Sandbox successfully removed from catalog", l.WithSandboxID(body.SandboxID))
	c.Status(http.StatusOK)
}
