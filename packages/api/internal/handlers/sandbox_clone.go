package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDClone(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()
	// Get team from context, use TeamContextKey

	teamID := a.GetTeamInfo(c).Team.ID

	sandboxID = utils.ShortID(sandboxID)

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)

	body, err := utils.ParseBody[api.PostSandboxesSandboxIDCloneJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	timeout := instance.InstanceExpiration
	if body.Timeout != nil {
		timeout = time.Duration(*body.Timeout) * time.Second

		if timeout > time.Duration(teamInfo.Tier.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Tier.MaxLengthHours))

			return
		}
	}

	isOriginalSbxRunning := true

	originalSbx, err := a.orchestrator.GetSandboxData(sandboxID, false)
	if err != nil {
		zap.L().Warn("Original sandbox not for clone not found", zap.Error(err), logger.WithSandboxID(sandboxID))

		isOriginalSbxRunning = false
	}

	storeConfig, err := a.orchestrator.CopySandboxToBucket(ctx, sandboxID, originalSbx.ClusterID, originalSbx.NodeID)
	if err != nil {
		zap.L().Error("Failed to copy sandbox to bucket", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to copy sandbox to bucket")

		return
	}

	if isOriginalSbxRunning && !originalSbx.IsExpired() {
		originalSandboxTimeout := time.Until(originalSbx.EndTime)

		_, createErr := a.startSandbox(
			ctx,
			originalSbx.SandboxID,
			originalSandboxTimeout,
			nil,
			originalSbx.Metadata,
			*originalSbx.Alias,
			teamInfo,
			queries.EnvBuild{
				ID: originalSbx.BuildID,
				EnvID: originalSbx.TemplateID,
				KernelVersion: originalSbx.KernelVersion,
				FirecrackerVersion: originalSbx.FirecrackerVersion,
				EnvdVersion: &originalSbx.EnvdVersion,
				RamMb: originalSbx.RamMB,
				Vcpu: originalSbx.VCpu,
				TotalDiskSizeMb: &originalSbx.TotalDiskSizeMB,
				ClusterNodeID: originalSbx.NodeID,
				
			},
			&c.Request.Header,
			true,
			nodeID,
			snap.BaseEnvID,
			autoPause,
			envdAccessToken,
			snap.AllowInternetAccess,
		)

		if createErr != nil {
			zap.L().Error("Failed to resume original cloned sandbox", zap.Error(createErr.Err))
			a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

			return
		}
	}

	clonedSandboxID := InstanceIDPrefix + id.Generate()

	clonedSbx, createErr := a.startSandbox(
		ctx,
		clonedSandboxID,
		timeout,
		nil,
		snap.Metadata,
		alias,
		teamInfo,
		build,
		&c.Request.Header,
		true,
		nodeID,
		snap.BaseEnvID,
		autoPause,
		envdAccessToken,
		snap.AllowInternetAccess,
	)

	if createErr != nil {
		zap.L().Error("Failed to clone sandbox", zap.Error(createErr.Err))
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

		return
	}

	c.JSON(http.StatusCreated, &clonedSbx)
}
