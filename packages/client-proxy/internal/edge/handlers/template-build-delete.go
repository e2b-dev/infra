package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	e2btemplatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1TemplateBuildDelete(c *gin.Context, buildId string, params api.V1TemplateBuildDeleteParams) {
	ctx := c.Request.Context()

	orchestrator, findErr := a.getTemplateManagerNode(params.OrchestratorId)
	if findErr != nil {
		a.sendAPIStoreError(c, findErr.prettyErrorCode, findErr.prettyErrorMessage)
		telemetry.ReportCriticalError(ctx, findErr.internalError)
		return
	}

	_, err := orchestrator.Client.Template.TemplateBuildDelete(
		ctx, &e2btemplatemanager.TemplateBuildDeleteRequest{
			TemplateID: params.TemplateId,
			BuildID:    buildId,
		},
	)

	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting template build")
		telemetry.ReportCriticalError(ctx, fmt.Errorf("error when deleting template build: %w", err))
		return
	}

	c.Status(http.StatusOK)
}
