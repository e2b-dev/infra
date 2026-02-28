package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const undefinedTableErrorCode = "42P01"

func (s *APIStore) GetSandboxesSandboxIDLog(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "get sandbox details")

	teamID := auth.MustGetTeamInfo(c).Team.ID
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()), telemetry.WithSandboxID(string(sandboxID)))

	row, err := s.db.GetSandboxDetailByTeamAndSandboxID(ctx, queries.GetSandboxDetailByTeamAndSandboxIDParams{
		TeamID:    teamID,
		SandboxID: sandboxID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isUndefinedTableError(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Sandbox not found or you don't have access to it")

			return
		}

		logger.L().Error(ctx, "Error getting sandbox details", zap.Error(err), logger.WithTeamID(teamID.String()), logger.WithSandboxID(string(sandboxID)))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting sandbox details")

		return
	}

	envdVersion := "v1.0.0"
	if row.EnvdVersion != nil && *row.EnvdVersion != "" {
		envdVersion = *row.EnvdVersion
	}

	var alias *string
	if row.Alias != "" {
		alias = &row.Alias
	}

	c.JSON(http.StatusOK, api.SandboxDetail{
		TemplateID:  row.TemplateID,
		Alias:       alias,
		SandboxID:   row.SandboxID,
		StartedAt:   row.StartedAt,
		StoppedAt:   row.StoppedAt,
		EnvdVersion: envdVersion,
		Domain:      row.Domain,
		CpuCount:    api.CPUCount(row.Vcpu),
		MemoryMB:    api.MemoryMB(row.RamMb),
		DiskSizeMB:  api.DiskSizeMB(row.TotalDiskSizeMb),
	})
}

func isUndefinedTableError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == undefinedTableErrorCode
}
