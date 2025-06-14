package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	grpcorchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1CreateSandbox(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := parseBody[api.V1CreateSandboxJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)
		return
	}

	_, templateSpan := a.tracer.Start(ctx, "create-sandbox-handler")
	defer templateSpan.End()

	orchestrator, findErr := a.getOrchestratorNode(body.Sandbox.OrchestratorId)
	if findErr != nil {
		a.sendAPIStoreError(c, findErr.prettyErrorCode, findErr.prettyErrorMessage)
		telemetry.ReportCriticalError(ctx, findErr.prettyErrorMessage, findErr.internalError)
		return
	}

	sbxMetadata := make(map[string]string)
	if body.Sandbox.Metadata != nil {
		sbxMetadata = *body.Sandbox.Metadata
	}

	sbxEnvVars := make(map[string]string)
	if body.Sandbox.EnvVars != nil {
		sbxEnvVars = *body.Sandbox.EnvVars
	}

	sbxBaseTemplateId := body.Sandbox.TemplateId
	if body.Sandbox.BaseTemplateId != nil {
		sbxBaseTemplateId = *body.Sandbox.BaseTemplateId
	}

	sbxRequest := &grpcorchestrator.SandboxCreateRequest{
		Sandbox: &grpcorchestrator.SandboxConfig{
			BaseTemplateId:     sbxBaseTemplateId,
			TemplateId:         body.Sandbox.TemplateId,
			BuildId:            body.Sandbox.BuildId,
			SandboxId:          body.Sandbox.SandboxId,
			Alias:              body.Sandbox.Alias,
			TeamId:             body.Sandbox.TeamId,
			KernelVersion:      body.Sandbox.KernelVersion,
			FirecrackerVersion: body.Sandbox.FirecrackerVersion,
			EnvdVersion:        body.Sandbox.EnvdVersion,
			EnvdAccessToken:    body.Sandbox.EnvdAccessToken,
			Metadata:           sbxMetadata,
			EnvVars:            sbxEnvVars,
			MaxSandboxLength:   24 * 60 * 60,
			HugePages:          body.Sandbox.HugePages,
			RamMb:              body.Sandbox.RamMB,
			Vcpu:               body.Sandbox.VCPU,
			Snapshot:           body.Sandbox.Snapshot,
			AutoPause:          body.Sandbox.AutoPause,
		},
		StartTime: timestamppb.New(body.StartTime.UTC()),
		EndTime:   timestamppb.New(body.EndTime.UTC()),
	}

	sbxResponse, err := orchestrator.Client.Sandbox.Create(ctx, sbxRequest)
	if err != nil {
		zap.L().Error("Error when creating sandbox", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when creating sandbox")
		telemetry.ReportCriticalError(ctx, "error when creating sandbox", err)
		return
	}

	err = a.sandboxes.StoreSandbox(
		body.Sandbox.SandboxId,
		&sandboxes.SandboxInfo{
			OrchestratorId: body.Sandbox.OrchestratorId,
			TemplateId:     body.Sandbox.TemplateId,
		},
	)

	if err != nil {
		zap.L().Error("Error when storing sandbox metadata", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when storing sandbox metadata")
		telemetry.ReportCriticalError(ctx, "error when storing sandbox metadata", err)
		return
	}

	zap.L().Info("Created sandbox", l.WithSandboxID(body.Sandbox.SandboxId))

	c.JSON(
		http.StatusCreated,
		api.SandboxCreateResponse{ClientId: sbxResponse.ClientId},
	)
}
