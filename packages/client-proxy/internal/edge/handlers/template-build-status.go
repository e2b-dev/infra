package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	e2btemplatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1TemplateBuildStatus(c *gin.Context, buildId string, params api.V1TemplateBuildStatusParams) {
	ctx := c.Request.Context()

	_, templateSpan := a.tracer.Start(c, "template-build-status-handler")
	defer templateSpan.End()

	orchestrator, findErr := a.getTemplateManagerNode(params.OrchestratorId)
	if findErr != nil {
		a.sendAPIStoreError(c, findErr.prettyErrorCode, findErr.prettyErrorMessage)
		telemetry.ReportCriticalError(ctx, findErr.prettyErrorMessage, findErr.internalError)
		return
	}

	resp, err := orchestrator.Client.Template.TemplateBuildStatus(
		ctx, &e2btemplatemanager.TemplateStatusRequest{
			TemplateID: params.TemplateId,
			BuildID:    buildId,
		},
	)

	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when fetching template build status")
		telemetry.ReportCriticalError(ctx, "error when fetching template build", err)
		return
	}

	var status = api.TemplateBuildStatusResponseStatusBuilding

	switch resp.Status {
	case e2btemplatemanager.TemplateBuildState_Building:
		status = api.TemplateBuildStatusResponseStatusBuilding
	case e2btemplatemanager.TemplateBuildState_Completed:
		status = api.TemplateBuildStatusResponseStatusReady
	case e2btemplatemanager.TemplateBuildState_Failed:
		status = api.TemplateBuildStatusResponseStatusError
	default:
		zap.L().Error("Unknown template build status", zap.String("status", resp.Status.String()))
	}

	var metadata *api.TemplateBuildMetadata
	if resp.Metadata != nil {
		metadata = &api.TemplateBuildMetadata{
			RootfsSizeKey:  resp.Metadata.RootfsSizeKey,
			EnvdVersionKey: resp.Metadata.EnvdVersionKey,
		}
	}

	c.JSON(
		http.StatusOK,
		api.TemplateBuildStatusResponse{
			TemplateID: params.TemplateId,
			BuildID:    buildId,
			Metadata:   metadata,
			Status:     status,
		},
	)
}
