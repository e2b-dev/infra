package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// UpsertProjectLimits records a project's effective limits.
//
// The values arrive absolute. This side stores them and does no arithmetic:
// the caller owns plans and add-ons and has already resolved them, which is
// what lets team_limits prefer this row over the tier it would otherwise
// compute from.
//
// Idempotent, because the caller retries. A repeated push writes the same nine
// values and touches updated_at, so a duplicate delivery is indistinguishable
// from the first.
func (s *APIStore) UpsertProjectLimits(c *gin.Context, teamID api.TeamID) {
	ctx := c.Request.Context()
	attrs := []attribute.KeyValue{telemetry.WithTeamID(teamID.String())}

	body, err := ginutils.ParseBody[api.AdminControlPlaneProjectLimits](ctx, c)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "upsert project limits failed",
			fmt.Errorf("parse project limits request: %w", err), attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	err = s.db.UpsertProjectLimits(ctx, queries.UpsertProjectLimitsParams{
		TeamID:                   teamID,
		MaxLengthHours:           int64(body.MaxSandboxLengthHours),
		ConcurrentSandboxes:      int64(body.ConcurrentSandboxes),
		ConcurrentTemplateBuilds: int64(body.ConcurrentTemplateBuilds),
		MaxVcpu:                  int64(body.MaxVcpu),
		MaxRamMb:                 body.MaxRamMb,
		DiskMb:                   body.DiskMb,
		EventsTtlDays:            int64(body.EventsTtlDays),
		DefaultFreeDiskSizeMb:    body.DefaultFreeDiskSizeMb,
		MaxDiskSizeMb:            body.MaxDiskSizeMb,
	})
	if err != nil {
		switch {
		// The only foreign key on the row is the team, so a violation says the
		// project is unknown here rather than that the payload was wrong.
		case dberrors.IsForeignKeyViolation(err):
			telemetry.ReportErrorByCode(ctx, http.StatusNotFound, "upsert project limits failed", err, attrs...)
			s.sendAPIStoreError(c, http.StatusNotFound, "Project not found")

		// Cross-column rules the request schema cannot express, currently that
		// the free disk allowance sits at or below the ceiling. The caller sent
		// a pair this side will not store, which is its error to fix.
		case dberrors.IsCheckViolation(err):
			telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "upsert project limits failed", err, attrs...)
			s.sendAPIStoreError(c, http.StatusBadRequest, "Limits violate a constraint")

		default:
			telemetry.ReportCriticalError(ctx, "upsert project limits failed", err, attrs...)
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Error updating project limits")
		}

		return
	}

	// Limits are cached with the team, so without this the write is invisible
	// until the entry expires. Logged rather than returned: the row is already
	// committed, and answering with an error would invite a retry that cannot
	// improve on a stale cache.
	if err := s.authService.InvalidateTeamCache(ctx, teamID); err != nil {
		logger.L().Error(ctx, "invalidating team cache after limits update",
			logger.WithTeamID(teamID.String()), zap.Error(err))
	}

	c.Status(http.StatusNoContent)
}
