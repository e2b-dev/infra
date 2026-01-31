package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	snapshotsDefaultLimit = 100
	snapshotsMaxLimit     = 100
	maxSnapshotID         = "zzzzzzzzzzzzzzzzzzzzzzzz"
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

	pagination, err := utils.NewPagination[queries.ListTeamSnapshotsRow](
		utils.PaginationParams{
			Limit:     params.Limit,
			NextToken: params.NextToken,
		},
		utils.PaginationConfig{
			DefaultLimit: snapshotsDefaultLimit,
			MaxLimit:     snapshotsMaxLimit,
			DefaultID:    maxSnapshotID,
		},
	)
	if err != nil {
		telemetry.ReportError(ctx, "error parsing pagination cursor", err)
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid next token")

		return
	}

	snapshots, err := a.sqlcDB.ListTeamSnapshots(ctx, queries.ListTeamSnapshotsParams{
		TeamID:     teamID,
		SandboxID:  params.SandboxID,
		CursorTime: pagination.CursorTime(),
		CursorID:   pagination.CursorID(),
		PageLimit:  pagination.QueryLimit(),
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "Error listing snapshots", err, telemetry.WithTeamID(teamID.String()))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error listing snapshots")

		return
	}

	snapshots = pagination.ProcessResultsWithHeader(c, snapshots, func(s queries.ListTeamSnapshotsRow) (time.Time, string) {
		return s.CreatedAt, s.SnapshotID
	})

	result := make([]api.SnapshotInfo, 0, len(snapshots))
	for _, snap := range snapshots {
		result = append(result, api.SnapshotInfo{SnapshotID: snap.SnapshotID})
	}

	c.JSON(http.StatusOK, result)
}
