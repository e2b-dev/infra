package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	grpcorchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1CreateSandbox(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := parseBody[api.V1CreateSandboxJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	orchestrator, err := a.orchestratorPool.GetOrchestrator(body.Sandbox.OrchestratorId)
	if err != nil {
		if errors.Is(err, pool.ErrOrchestratorNotFound) {
			a.sendAPIStoreError(c, http.StatusBadRequest, "Orchestrator not found")
			telemetry.ReportCriticalError(ctx, fmt.Errorf("orchestrator not found: %w", err))
			return
		}

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting orchestrator")
		telemetry.ReportCriticalError(ctx, fmt.Errorf("error when getting orchestrator: %w", err))
		return
	}

	if orchestrator.Status != pool.OrchestratorStatusHealthy {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Orchestrator is not ready for sandbox placement")
		telemetry.ReportCriticalError(ctx, fmt.Errorf("orchestrator is not ready for sandbox placement: %w", err))
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
		errMsg := fmt.Errorf("error when creating sandbox: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
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
		errMsg := fmt.Errorf("error when storing sandbox metadata: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	zap.L().Info("Created sandbox", zap.String("sandbox_id", body.Sandbox.SandboxId), zap.String("client_id", sbxResponse.ClientId))

	c.JSON(
		http.StatusOK,
		api.SandboxCreateResponse{ClientId: sbxResponse.ClientId},
	)
}
