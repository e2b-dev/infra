package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardutils "github.com/e2b-dev/infra/packages/dashboard-api/internal/utils"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	defaultBuildsLimit = int32(50)
	maxBuildsLimit     = int32(100)
	maxCursorID        = "ffffffff-ffff-ffff-ffff-ffffffffffff"
)

var defaultBuildStatuses = []string{
	string(api.BuildStatusDBWaiting),
	string(api.BuildStatusDBBuilding),
	string(api.BuildStatusDBUploaded),
	string(api.BuildStatusDBFailed),
}

func (s *APIStore) GetBuilds(c *gin.Context, params api.GetBuildsParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list builds")

	teamID := auth.MustGetTeamInfo(c).Team.ID
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))

	limit := normalizeBuildsLimit(params.Limit)
	cursorTime, cursorID, err := parseBuildsCursor(params.Cursor)
	if err != nil {
		logger.L().Warn(ctx, "invalid builds cursor", zap.Error(err), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusBadRequest, "invalid cursor")

		return
	}

	statuses := normalizeBuildStatuses(params.Statuses)
	rows, err := s.listBuildRows(ctx, teamID, params.BuildIdOrTemplate, statuses, cursorTime, cursorID, limit+1)
	if err != nil {
		logger.L().Error(ctx, "Error getting builds", zap.Error(err), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting builds")

		return
	}

	hasMore := len(rows) > int(limit)
	if hasMore {
		rows = rows[:limit]
	}

	builds := make([]api.ListedBuild, 0, len(rows))
	for _, row := range rows {
		template := row.TemplateAlias
		if template == "" {
			template = row.TemplateID
		}

		builds = append(builds, api.ListedBuild{
			Id:            row.ID,
			Template:      template,
			TemplateId:    row.TemplateID,
			Status:        dashboardutils.MapBuildStatusFromDBStatus(row.Status),
			StatusMessage: dashboardutils.MapBuildStatusMessageFromDBStatus(row.Status, row.Reason),
			CreatedAt:     row.CreatedAt,
			FinishedAt:    row.FinishedAt,
		})
	}

	var nextCursor *string
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		cursor := fmt.Sprintf("%s|%s", last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID.String())
		nextCursor = &cursor
	}

	c.JSON(http.StatusOK, api.BuildsListResponse{
		Data:       builds,
		NextCursor: nextCursor,
	})
}

type listBuildRow struct {
	ID            uuid.UUID
	Status        dbtypes.BuildStatus
	Reason        []byte
	CreatedAt     time.Time
	FinishedAt    *time.Time
	TemplateID    string
	TemplateAlias string
}

func (s *APIStore) listBuildRows(
	ctx context.Context,
	teamID uuid.UUID,
	buildIDOrTemplate *string,
	statuses []string,
	cursorTime time.Time,
	cursorID uuid.UUID,
	limitPlusOne int32,
) ([]listBuildRow, error) {
	if buildIDOrTemplate == nil || strings.TrimSpace(*buildIDOrTemplate) == "" {
		rows, err := s.db.GetTeamBuildsPage(ctx, queries.GetTeamBuildsPageParams{
			TeamID:          teamID,
			CursorCreatedAt: cursorTime,
			CursorID:        cursorID,
			Statuses:        statuses,
			LimitPlusOne:    limitPlusOne,
		})
		if err != nil {
			return nil, err
		}

		return mapBuildRows(rows), nil
	}

	filter := strings.TrimSpace(*buildIDOrTemplate)
	filterUUID, parseErr := uuid.Parse(filter)
	if parseErr == nil {
		byBuildIDRows, byBuildIDErr := s.db.GetTeamBuildsPageByBuildID(ctx, queries.GetTeamBuildsPageByBuildIDParams{
			TeamID:          teamID,
			BuildID:         filterUUID,
			CursorCreatedAt: cursorTime,
			CursorID:        cursorID,
			Statuses:        statuses,
			LimitPlusOne:    limitPlusOne,
		})
		if byBuildIDErr != nil {
			return nil, byBuildIDErr
		}
		if len(byBuildIDRows) > 0 {
			return mapBuildRowsByBuildID(byBuildIDRows), nil
		}
	}

	byTemplateIDRows, byTemplateIDErr := s.db.GetTeamBuildsPageByTemplateID(ctx, queries.GetTeamBuildsPageByTemplateIDParams{
		TemplateID:      filter,
		TeamID:          teamID,
		CursorCreatedAt: cursorTime,
		CursorID:        cursorID,
		Statuses:        statuses,
		LimitPlusOne:    limitPlusOne,
	})
	if byTemplateIDErr != nil {
		return nil, byTemplateIDErr
	}
	if len(byTemplateIDRows) > 0 {
		return mapBuildRowsByTemplateID(byTemplateIDRows), nil
	}

	byTemplateAliasRows, byTemplateAliasErr := s.db.GetTeamBuildsPageByTemplateAlias(ctx, queries.GetTeamBuildsPageByTemplateAliasParams{
		TemplateAlias:   filter,
		TeamID:          teamID,
		CursorCreatedAt: cursorTime,
		CursorID:        cursorID,
		Statuses:        statuses,
		LimitPlusOne:    limitPlusOne,
	})
	if byTemplateAliasErr != nil {
		return nil, byTemplateAliasErr
	}

	return mapBuildRowsByTemplateAlias(byTemplateAliasRows), nil
}

func normalizeBuildsLimit(limit *api.BuildsLimit) int32 {
	if limit == nil {
		return defaultBuildsLimit
	}

	if *limit < 1 {
		return 1
	}

	if *limit > maxBuildsLimit {
		return maxBuildsLimit
	}

	return int32(*limit)
}

func normalizeBuildStatuses(statuses *api.BuildStatuses) []string {
	if statuses == nil || len(*statuses) == 0 {
		return defaultBuildStatuses
	}

	normalized := make([]string, 0, len(*statuses))
	for _, status := range *statuses {
		normalized = append(normalized, string(status))
	}

	return normalized
}

func parseBuildsCursor(cursor *api.BuildsCursor) (time.Time, uuid.UUID, error) {
	defaultID := uuid.MustParse(maxCursorID)
	if cursor == nil || *cursor == "" {
		return time.Now().UTC(), defaultID, nil
	}

	parts := strings.SplitN(*cursor, "|", 2)
	if len(parts) == 1 {
		cursorTime, err := parseCursorTime(parts[0])
		if err != nil {
			return time.Time{}, uuid.Nil, err
		}

		return cursorTime, defaultID, nil
	}

	cursorTime, err := parseCursorTime(parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}

	cursorID, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}

	return cursorTime, cursorID, nil
}

func parseCursorTime(value string) (time.Time, error) {
	cursorTime, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return cursorTime, nil
	}

	return time.Parse(time.RFC3339, value)
}

func mapBuildRows(rows []queries.GetTeamBuildsPageRow) []listBuildRow {
	out := make([]listBuildRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, listBuildRow{
			ID:            row.ID,
			Status:        row.Status,
			Reason:        row.Reason,
			CreatedAt:     row.CreatedAt,
			FinishedAt:    row.FinishedAt,
			TemplateID:    row.TemplateID,
			TemplateAlias: row.TemplateAlias,
		})
	}

	return out
}

func mapBuildRowsByBuildID(rows []queries.GetTeamBuildsPageByBuildIDRow) []listBuildRow {
	out := make([]listBuildRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, listBuildRow{
			ID:            row.ID,
			Status:        row.Status,
			Reason:        row.Reason,
			CreatedAt:     row.CreatedAt,
			FinishedAt:    row.FinishedAt,
			TemplateID:    row.TemplateID,
			TemplateAlias: row.TemplateAlias,
		})
	}

	return out
}

func mapBuildRowsByTemplateID(rows []queries.GetTeamBuildsPageByTemplateIDRow) []listBuildRow {
	out := make([]listBuildRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, listBuildRow{
			ID:            row.ID,
			Status:        row.Status,
			Reason:        row.Reason,
			CreatedAt:     row.CreatedAt,
			FinishedAt:    row.FinishedAt,
			TemplateID:    row.TemplateID,
			TemplateAlias: row.TemplateAlias,
		})
	}

	return out
}

func mapBuildRowsByTemplateAlias(rows []queries.GetTeamBuildsPageByTemplateAliasRow) []listBuildRow {
	out := make([]listBuildRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, listBuildRow{
			ID:            row.ID,
			Status:        row.Status,
			Reason:        row.Reason,
			CreatedAt:     row.CreatedAt,
			FinishedAt:    row.FinishedAt,
			TemplateID:    row.TemplateID,
			TemplateAlias: row.TemplateAlias,
		})
	}

	return out
}
