package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/events"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// Monitoring (metrics) and logs data has a fixed retention window,
	// independent of the team's events retention limit.
	monitoringRetention = 7 * 24 * time.Hour

	undefinedTableErrorCode = "42P01"
)

func (s *APIStore) GetSandboxesSandboxIDRecord(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "get sandbox details")

	team := auth.MustGetTeamInfo(c)
	teamID := team.Team.ID
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()), telemetry.WithSandboxID(sandboxID))

	row, err := s.db.GetSandboxRecordByTeamAndSandboxID(ctx, queries.GetSandboxRecordByTeamAndSandboxIDParams{
		TeamID:    teamID,
		SandboxID: sandboxID,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) || isUndefinedTableError(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Sandbox not found or you don't have access to it")

			return
		}

		logger.L().Error(ctx, "Error getting sandbox details", zap.Error(err), logger.WithTeamID(teamID.String()), logger.WithSandboxID(sandboxID))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting sandbox details")

		return
	}

	var alias *string
	if row.Alias != "" {
		alias = &row.Alias
	}

	// Monitoring and logs data is purged once the sandbox ended more than the
	// fixed retention window ago.
	retentionExpired := row.StoppedAt != nil && time.Since(*row.StoppedAt) > monitoringRetention

	// Events retention comes from the team's limits (tier + addons)
	eventsRetentionDays := min(team.Limits.EventsTTLDays, events.MaxEventsTTLDays)
	eventsRetention := time.Duration(eventsRetentionDays) * 24 * time.Hour
	eventsRetentionExpired := row.StoppedAt != nil && time.Since(*row.StoppedAt) > eventsRetention

	c.JSON(http.StatusOK, api.SandboxRecord{
		TemplateID:             row.TemplateID,
		Alias:                  alias,
		SandboxID:              row.SandboxID,
		StartedAt:              row.StartedAt,
		StoppedAt:              row.StoppedAt,
		Domain:                 row.Domain,
		CpuCount:               row.Vcpu,
		MemoryMB:               row.RamMb,
		DiskSizeMB:             row.TotalDiskSizeMb,
		RetentionExpired:       retentionExpired,
		EventsRetentionExpired: eventsRetentionExpired,
	})
}

func isUndefinedTableError(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == undefinedTableErrorCode
}
