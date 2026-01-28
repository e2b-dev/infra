package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	typesteam "github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const defaultSnapshotPageLimit = 100

func (a *APIStore) GetSnapshots(c *gin.Context, params api.GetSnapshotsParams) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(*typesteam.Team)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	pageLimit := int32(defaultSnapshotPageLimit)
	if params.Limit != nil && *params.Limit > 0 && *params.Limit <= 100 {
		pageLimit = *params.Limit
	}

	pageOffset := int32(0)
	if params.NextToken != nil && *params.NextToken != "" {
		// For simplicity, use offset as token
		// In production, use a cursor-based approach
		offset := 0
		_, err := fmt.Sscanf(*params.NextToken, "%d", &offset)
		if err == nil {
			pageOffset = int32(offset)
		}
	}

	sourceSandboxID := ""
	if params.SandboxID != nil {
		sourceSandboxID = *params.SandboxID
	}

	snapshots, err := a.sqlcDB.ListTeamSnapshots(ctx, queries.ListTeamSnapshotsParams{
		TeamID:          teamID,
		SourceSandboxID: sourceSandboxID,
		PageOffset:      pageOffset,
		PageLimit:       pageLimit,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusOK, struct {
				Snapshots []api.SnapshotInfo `json:"snapshots"`
			}{
				Snapshots: []api.SnapshotInfo{},
			})
			return
		}
		logger.L().Error(ctx, "Error listing snapshots", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error listing snapshots")
		return
	}

	result := make([]api.SnapshotInfo, 0, len(snapshots))
	for _, snap := range snapshots {
		cpuCount := api.CPUCount(int32(snap.Vcpu))
		memoryMB := api.MemoryMB(int32(snap.RamMb))
		var diskSizeMB *api.DiskSizeMB
		if snap.TotalDiskSizeMb != nil {
			d := api.DiskSizeMB(int32(*snap.TotalDiskSizeMb))
			diskSizeMB = &d
		}

		result = append(result, api.SnapshotInfo{
			SnapshotID:  snap.SnapshotID,
			SandboxID:   snap.SourceSandboxID,
			CreatedAt:   snap.CreatedAt,
			CpuCount:    &cpuCount,
			MemoryMB:    &memoryMB,
			DiskSizeMB:  diskSizeMB,
		})
	}

	var nextToken *string
	if len(snapshots) == int(pageLimit) {
		token := fmt.Sprintf("%d", pageOffset+pageLimit)
		nextToken = &token
	}

	c.JSON(http.StatusOK, struct {
		Snapshots []api.SnapshotInfo `json:"snapshots"`
		NextToken *string            `json:"nextToken,omitempty"`
	}{
		Snapshots: result,
		NextToken: nextToken,
	})
}
