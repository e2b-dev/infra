package handlers

import (
	"github.com/gin-gonic/gin"
)

func (a *APIStore) V1CreateSandbox(c *gin.Context) {
	/*
		ctx := c.Request.Context()

		body, err := parseBody[api.V1CreateSandboxJSONRequestBody](ctx, c)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
			errMsg := fmt.Errorf("error when parsing request: %w", err)
			telemetry.ReportCriticalError(ctx, errMsg)
			return
		}

		// todo
		teamId := uuid.NewString()
		alias := "base"
		autoPause := false

		startTime := time.Now()
		endTime := startTime.Add(2 * time.Minute)

		sbxRequest := &orchestrator.SandboxCreateRequest{
			Sandbox: &orchestrator.SandboxConfig{
				BaseTemplateId:     body.Sandbox.TemplateId, // todo??
				TemplateId:         body.Sandbox.TemplateId,
				BuildId:            body.Sandbox.BuildId,
				SandboxId:          body.Sandbox.SandboxId,
				Alias:              &alias,
				TeamId:             teamId,
				KernelVersion:      body.Sandbox.KernelVersion,
				FirecrackerVersion: body.Sandbox.FirecrackerVersion,
				EnvdVersion:        body.Sandbox.EnvdVersion,
				Metadata:           make(map[string]string),
				EnvVars:            make(map[string]string),
				EnvdAccessToken:    nil,
				MaxSandboxLength:   24 * 60 * 60,
				HugePages:          body.Sandbox.HugePages,
				RamMb:              1024 * 1024,
				Vcpu:               4,
				Snapshot:           false,
				AutoPause:          &autoPause,
			},
			StartTime: timestamppb.New(startTime),
			EndTime:   timestamppb.New(endTime),
		}

		o, err := a.orchestratorsPool.GetNode(body.Sandbox.OrchestratorId)
		if err != nil {
			if errors.Is(err, orchestratorPool.ErrOrchestratorNotFound) {
				a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Orchestrator not found: %s", err))
				telemetry.ReportCriticalError(ctx, fmt.Errorf("orchestrator not found: %w", err))
				return
			}

			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting orchestrator: %s", err))
			telemetry.ReportCriticalError(ctx, fmt.Errorf("error when getting orchestrator: %w", err))
			return
		}

		_, err = o.Client.Sandbox.Create(ctx, sbxRequest)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when creating sandbox: %s", err))
			errMsg := fmt.Errorf("error when creating sandbox: %w", err)
			telemetry.ReportCriticalError(ctx, errMsg)
			return
		}

		c.Status(http.StatusCreated)

	*/
}
