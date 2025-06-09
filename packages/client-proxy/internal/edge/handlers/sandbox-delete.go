package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	grpcorchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1DeleteSandbox(c *gin.Context, sandboxId api.SandboxId) {
	ctx := c.Request.Context()

	sbx, err := a.sandboxes.GetSandbox(sandboxId)
	if err != nil {
		if errors.Is(err, sandboxes.SandboxNotFoundError) {
			a.sendAPIStoreError(c, http.StatusNotFound, "Sandbox not found")
			telemetry.ReportCriticalError(ctx, fmt.Errorf("sandbox not found: %w", err))
			return
		}

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting sandbox")
		telemetry.ReportCriticalError(ctx, fmt.Errorf("error when getting sandbox: %w", err))
		return
	}

	orchestrator, findErr := a.getOrchestratorNode(sbx.OrchestratorId)
	if findErr != nil {
		a.sendAPIStoreError(c, findErr.prettyErrorCode, findErr.prettyErrorMessage)
		telemetry.ReportCriticalError(ctx, findErr.internalError)
		return
	}

	_, err = orchestrator.Client.Sandbox.Delete(ctx, &grpcorchestrator.SandboxDeleteRequest{SandboxId: sandboxId})
	if err != nil {
		zap.L().Error("Error when deleting sandbox", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting sandbox")
		errMsg := fmt.Errorf("error when deleting sandbox: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	zap.L().Info("Sandbox deleted", zap.String("sandbox_id", sandboxId))

	c.Status(http.StatusOK)
}
