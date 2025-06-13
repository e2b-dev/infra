package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1ListSandboxes(c *gin.Context, params api.V1ListSandboxesParams) {
	ctx := c.Request.Context()

	_, templateSpan := a.tracer.Start(ctx, "list-sandboxes-handler")
	defer templateSpan.End()

	orchestrator, findErr := a.getOrchestratorNode(params.OrchestratorId)
	if findErr != nil {
		a.sendAPIStoreError(c, findErr.prettyErrorCode, findErr.prettyErrorMessage)
		telemetry.ReportCriticalError(ctx, findErr.prettyErrorMessage, findErr.internalError)
		return
	}

	sandboxesRaw, err := orchestrator.Client.Sandbox.List(ctx, &emptypb.Empty{})
	if err != nil {
		zap.L().Error("Error when listing sandboxes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when listing sandboxes")
		telemetry.ReportCriticalError(ctx, "error when listing sandboxes", err)
		return
	}

	sandboxes := make([]api.RunningSandbox, 0, len(sandboxesRaw.Sandboxes))
	for _, sbx := range sandboxesRaw.Sandboxes {
		startTime := sbx.StartTime.AsTime()
		endTime := sbx.EndTime.AsTime()

		envVars := make(map[string]string)
		for k, v := range sbx.Config.EnvVars {
			envVars[k] = v
		}

		metadata := make(map[string]string)
		for k, v := range sbx.Config.Metadata {
			metadata[k] = v
		}

		conf := api.SandboxConfig{
			OrchestratorId: orchestrator.ServiceId,
			BuildId:        sbx.Config.BuildId,
			TeamId:         sbx.Config.TeamId,
			SandboxId:      sbx.Config.SandboxId,
			TemplateId:     sbx.Config.TemplateId,
			BaseTemplateId: &sbx.Config.BaseTemplateId,
			Alias:          sbx.Config.Alias,

			EnvVars:         &envVars,
			EnvdAccessToken: sbx.Config.EnvdAccessToken,
			EnvdVersion:     sbx.Config.EnvdVersion,

			HugePages:        sbx.Config.HugePages,
			MaxSandboxLength: sbx.Config.MaxSandboxLength,
			AutoPause:        sbx.Config.AutoPause,

			RamMB:           sbx.Config.RamMb,
			Snapshot:        sbx.Config.Snapshot,
			TotalDiskSizeMB: sbx.Config.TotalDiskSizeMb,
			VCPU:            sbx.Config.Vcpu,

			FirecrackerVersion: sbx.Config.FirecrackerVersion,
			KernelVersion:      sbx.Config.KernelVersion,
			Metadata:           &metadata,
		}

		sandboxes = append(
			sandboxes,
			api.RunningSandbox{
				ClientId:  &sbx.ClientId,
				Config:    &conf,
				StartTime: &startTime,
				EndTime:   &endTime,
			},
		)
	}

	c.Status(http.StatusOK)
	c.JSON(
		http.StatusCreated,
		api.SandboxListResponse{Sandboxes: sandboxes},
	)
}
