package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	grpcorchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1PauseSandbox(c *gin.Context, sandboxId api.SandboxId) {
	ctx := c.Request.Context()

	body, err := parseBody[api.V1PauseSandboxJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)
		return
	}

	_, templateSpan := a.tracer.Start(ctx, "pause-sandbox-handler")
	defer templateSpan.End()

	sbx, err := a.sandboxes.GetSandbox(sandboxId)
	if err != nil {
		if errors.Is(err, sandboxes.ErrSandboxNotFound) {
			a.sendAPIStoreError(c, http.StatusNotFound, "Sandbox not found")
			telemetry.ReportCriticalError(ctx, "sandbox not found", err)
			return
		}

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting sandbox")
		telemetry.ReportCriticalError(ctx, "error when getting sandbox", err)
		return
	}

	orchestrator, findErr := a.getOrchestratorNode(sbx.OrchestratorId)
	if findErr != nil {
		a.sendAPIStoreError(c, findErr.prettyErrorCode, findErr.prettyErrorMessage)
		telemetry.ReportCriticalError(ctx, findErr.prettyErrorMessage, findErr.internalError)
		return
	}

	_, err = orchestrator.Client.Sandbox.Pause(
		ctx,
		&grpcorchestrator.SandboxPauseRequest{
			SandboxId:  sandboxId,
			TemplateId: body.TemplateId,
			BuildId:    body.BuildId,
		},
	)

	if err != nil {
		zap.L().Error("Error when pausing sandbox", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when pausing sandbox")
		telemetry.ReportCriticalError(ctx, "error when pausing sandbox", err)
		return
	}

	err = a.sandboxes.DeleteSandbox(sandboxId)
	if err != nil {
		zap.L().Error("Error when deleting sandbox from cache", zap.Error(err))
	}

	zap.L().Info("Sandbox paused", l.WithSandboxID(sandboxId))
	c.Status(http.StatusOK)
}
