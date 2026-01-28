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
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	snapshotTemplatesDefaultLimit = 100
	snapshotTemplatesMaxLimit     = 100
	maxSnapshotTemplateID         = "zzzzzzzzzzzzzzzzzzzzzzzz"
)

func (a *APIStore) GetSnapshots(c *gin.Context, params api.GetSnapshotsParams) {
	ctx := c.Request.Context()

	teamInfo := a.GetTeamInfo(c)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))
	telemetry.ReportEvent(ctx, "Listing snapshot templates")

	pagination, err := utils.NewPagination[queries.ListTeamSnapshotTemplatesRow](
		utils.PaginationParams{
			Limit:     params.Limit,
			NextToken: params.NextToken,
		},
		utils.PaginationConfig{
			DefaultLimit: snapshotTemplatesDefaultLimit,
			MaxLimit:     snapshotTemplatesMaxLimit,
			DefaultID:    maxSnapshotTemplateID,
		},
	)
	if err != nil {
		telemetry.ReportError(ctx, "error parsing pagination cursor", err)
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid next token")

		return
	}

	var sandboxIDFilter *string
	if params.SandboxID != nil {
		short := utils.ShortID(*params.SandboxID)
		sandboxIDFilter = &short
	}

	snapshots, err := a.sqlcDB.ListTeamSnapshotTemplates(ctx, queries.ListTeamSnapshotTemplatesParams{
		TeamID:     teamID,
		SandboxID:  sandboxIDFilter,
		CursorTime: pagination.CursorTime(),
		CursorID:   pagination.CursorID(),
		PageLimit:  pagination.QueryLimit(),
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "Error listing snapshot templates", err, telemetry.WithTeamID(teamID.String()))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error listing snapshot templates")

		return
	}

	snapshots = pagination.ProcessResultsWithHeader(c, snapshots, func(s queries.ListTeamSnapshotTemplatesRow) (time.Time, string) {
		return s.CreatedAt, s.SnapshotID
	})

	result := sharedUtils.Map(snapshots, func(snap queries.ListTeamSnapshotTemplatesRow) api.SnapshotInfo {
		return api.SnapshotInfo{
			SnapshotID: snap.SnapshotID,
			Names:      snap.Names,
		}
	})

	c.JSON(http.StatusOK, result)
}
