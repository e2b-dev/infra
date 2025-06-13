package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	grpcorchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1DeleteSandbox(c *gin.Context, sandboxId api.SandboxId) {
	ctx := c.Request.Context()

	_, templateSpan := a.tracer.Start(ctx, "create-delete-handler")
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

	_, err = orchestrator.Client.Sandbox.Delete(ctx, &grpcorchestrator.SandboxDeleteRequest{SandboxId: sandboxId})
	if err != nil {
		zap.L().Error("Error when deleting sandbox", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting sandbox")
		telemetry.ReportCriticalError(ctx, "error when deleting sandbox", err)
		return
	}

	zap.L().Info("Sandbox deleted", l.WithSandboxID(sandboxId))
	c.Status(http.StatusOK)
}
