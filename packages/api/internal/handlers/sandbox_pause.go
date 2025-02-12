package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDPause(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()
	// Get team from context, use TeamContextKey

	teamID := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team.ID

	sandboxID = utils.ShortID(sandboxID)

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	sbx, err := a.orchestrator.GetSandbox(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error pausing sandbox - sandbox '%s' was not found", sandboxID))

		// TODO: Check if sandbox is already paused to return 409
		return
	}

	if *sbx.TeamID != teamID {
		errMsg := fmt.Errorf("sandbox '%s' does not belong to team '%s'", sandboxID, teamID.String())
		telemetry.ReportCriticalError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusUnauthorized, fmt.Sprintf("Error pausing sandbox - sandbox '%s' does not belong to your team '%s'", sandboxID, teamID.String()))

		return
	}

	found := a.orchestrator.DeleteInstance(ctx, sandboxID, true)
	if !found {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error pausing sandbox - sandbox '%s' was not found", sandboxID))
		return
	}

	err, ok := <-sbx.AutoPauseCh
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error pausing sandbox: %s", err))
		return
	}
	if !ok {
		a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Error pausing sandbox - sandbox '%s' is already paused", sandboxID))
		return
	}

	c.Status(http.StatusNoContent)
}
