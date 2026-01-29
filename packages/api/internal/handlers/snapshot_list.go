package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	defaultSnapshotPageLimit = 100
	maxSnapshotPageOffset    = 10000
)

func (a *APIStore) GetSnapshots(c *gin.Context, params api.GetSnapshotsParams) {
	ctx := c.Request.Context()

	teamInfo := a.GetTeamInfo(c)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))
	telemetry.ReportEvent(ctx, "Listing snapshots")

	pageLimit := int32(defaultSnapshotPageLimit)
	if params.Limit != nil && *params.Limit > 0 && *params.Limit <= 100 {
		pageLimit = *params.Limit
	}

	pageOffset := int32(0)
	if params.NextToken != nil && *params.NextToken != "" {
		offset, err := strconv.ParseInt(*params.NextToken, 10, 32)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid pagination token")

			return
		}
		if offset < 0 || offset > maxSnapshotPageOffset {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Pagination offset must be between 0 and %d", maxSnapshotPageOffset))

			return
		}
		pageOffset = int32(offset)
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
		logger.L().Error(ctx, "Error listing snapshots", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error listing snapshots")

		return
	}

	result := make([]api.SnapshotInfo, 0, len(snapshots))
	for _, snap := range snapshots {
		result = append(result, *buildSnapshotInfo(
			snap.SnapshotID,
			snap.SourceSandboxID,
			snap.BaseTemplateID,
			snap.CreatedAt,
			snap.Vcpu,
			snap.RamMb,
			snap.TotalDiskSizeMb,
		))
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
