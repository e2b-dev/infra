package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (a *APIStore) GetSnapshotsSnapshotID(c *gin.Context, snapshotID api.SnapshotID) {
	ctx := c.Request.Context()

	teamInfo := a.GetTeamInfo(c)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))
	telemetry.ReportEvent(ctx, "Getting snapshot")

	snapshot, err := a.sqlcDB.GetSnapshotTemplate(ctx, queries.GetSnapshotTemplateParams{
		SnapshotID: snapshotID,
		TeamID:     teamID,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Snapshot '%s' not found", snapshotID))

			return
		}
		logger.L().Error(ctx, "Error getting snapshot", zap.Error(err), zap.String("snapshot_id", snapshotID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error getting snapshot")

		return
	}

	c.JSON(http.StatusOK, buildSnapshotInfo(
		snapshot.SnapshotID,
		snapshot.SourceSandboxID,
		snapshot.BaseTemplateID,
		snapshot.CreatedAt,
		snapshot.Vcpu,
		snapshot.RamMb,
		snapshot.TotalDiskSizeMb,
	))
}

func buildSnapshotInfo(snapshotID string, sandboxID, templateID *string, createdAt time.Time, vcpu, ramMb int64, totalDiskSizeMb *int64) *api.SnapshotInfo {
	cpuCount := api.CPUCount(vcpu)
	memoryMB := api.MemoryMB(ramMb)
	diskSizeMB := sharedUtils.CastPtr(totalDiskSizeMb, func(v int64) api.DiskSizeMB { return api.DiskSizeMB(v) })

	return &api.SnapshotInfo{
		SnapshotID: snapshotID,
		SandboxID:  sandboxID,
		TemplateID: templateID,
		CreatedAt:  createdAt,
		CpuCount:   &cpuCount,
		MemoryMB:   &memoryMB,
		DiskSizeMB: diskSizeMB,
	}
}
