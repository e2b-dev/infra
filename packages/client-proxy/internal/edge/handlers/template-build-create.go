package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	e2btemplatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1TemplateBuildCreate(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := parseBody[api.TemplateBuildCreateRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)
		return
	}

	orchestrator, findErr := a.getTemplateManagerNode(body.OrchestratorId)
	if findErr != nil {
		a.sendAPIStoreError(c, findErr.prettyErrorCode, findErr.prettyErrorMessage)
		telemetry.ReportCriticalError(ctx, findErr.prettyErrorMessage, findErr.internalError)
		return
	}

	_, err = orchestrator.Client.Template.TemplateCreate(
		ctx, &e2btemplatemanager.TemplateCreateRequest{
			Template: &e2btemplatemanager.TemplateConfig{
				BuildID:    body.BuildId,
				TemplateID: body.TemplateId,
				MemoryMB:   int32(body.RamMB),
				VCpuCount:  int32(body.VCPU),
				DiskSizeMB: int32(body.DiskSizeMB),
				HugePages:  body.HugePages,

				KernelVersion:      body.KernelVersion,
				FirecrackerVersion: body.FirecrackerVersion,

				StartCommand: body.StartCommand,
				ReadyCommand: body.ReadyCommand,
			},
		},
	)

	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating template: %s", err))
		telemetry.ReportCriticalError(ctx, "error when creating template build", err)
		return
	}

	c.Status(http.StatusOK)
}
