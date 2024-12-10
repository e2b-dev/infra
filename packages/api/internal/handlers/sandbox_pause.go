package handlers

import (
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
)

func (a *APIStore) PostSandboxesSandboxIDPause(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()
	// Get team from context, use TeamContextKey

	teamID := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team.ID

	sandboxID = utils.ShortID(sandboxID)

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	sbx, err := a.orchestrator.GetSandbox(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error pausing sandbox - sandbox '%s' was not found", sandboxID))

		// TODO: Check if sandbox is already paused to return 409
		return
	}

	if *sbx.TeamID != teamID {
		errMsg := fmt.Errorf("sandbox '%s' does not belong to team '%s'", sandboxID, teamID.String())
		telemetry.ReportCriticalError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusUnauthorized, fmt.Sprintf("Error pausing sandbox - sandbox '%s' does not belong to your team '%s'", sandboxID, teamID.String()))

		return
	}

	snapshotConfig := &db.SnapshotInfo{
		SandboxID:          sandboxID,
		VCPU:               sbx.VCpu,
		RAMMB:              sbx.RamMB,
		TotalDiskSizeMB:    sbx.TotalDiskSizeMB,
		Metadata:           sbx.Metadata,
		KernelVersion:      sbx.KernelVersion,
		FirecrackerVersion: sbx.FirecrackerVersion,
		EnvdVersion:        sbx.Instance.EnvdVersion,
	}

	envBuild, err := a.db.NewSnapshotBuild(
		ctx,
		snapshotConfig,
		teamID,
		a.logger,
	)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error pausing sandbox: %s", err))

		return
	}

	err = a.orchestrator.PauseInstance(ctx, sbx, *envBuild.EnvID, envBuild.ID.String())
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error pausing sandbox: %s", err))

		return
	}

	err = a.db.EnvBuildSetStatus(ctx, *envBuild.EnvID, envBuild.ID, envbuild.StatusSuccess)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error pausing sandbox: %s", err))

		return
	}

	c.Status(http.StatusNoContent)
}
