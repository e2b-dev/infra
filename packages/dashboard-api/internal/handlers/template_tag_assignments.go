package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	defaultTagAssignmentsLimit = int32(50)
	maxTagAssignmentsLimit     = int32(100)
)

func (s *APIStore) GetTemplatesTemplateIDTagsTagAssignments(c *gin.Context, templateID api.TemplateID, tag api.TagPath, params api.GetTemplatesTemplateIDTagsTagAssignmentsParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list template tag assignments")

	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()), telemetry.WithTemplateID(templateID))

	if !s.requireTemplateAccess(c, templateID, teamID) {
		return
	}

	normalizedTags, err := id.ValidateAndDeduplicateTags([]string{tag})
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid tag")

		return
	}
	normalizedTag := normalizedTags[0]

	limit := normalizeTagAssignmentsLimit(params.Limit)
	cursorTime, cursorID, err := parseTagAssignmentsCursor(params.Cursor)
	if err != nil {
		logger.L().Warn(ctx, "invalid tag assignments cursor", zap.Error(err), logger.WithTeamID(teamID.String()), logger.WithTemplateID(templateID))
		s.sendAPIStoreError(c, http.StatusBadRequest, "invalid cursor")

		return
	}

	rows, err := s.db.Dashboard.ListTemplateTagAssignmentsByTag(ctx, dashboardqueries.ListTemplateTagAssignmentsByTagParams{
		TemplateID:         templateID,
		Tag:                normalizedTag,
		CursorAssignedAt:   cursorTime,
		CursorAssignmentID: cursorID,
		LimitPlusOne:       limit + 1,
	})
	if err != nil {
		logger.L().Error(ctx, "Error getting template tag assignments", zap.Error(err), logger.WithTeamID(teamID.String()), logger.WithTemplateID(templateID))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template tag assignments")

		return
	}

	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}

	assignments := make([]api.TemplateTagAssignment, 0, len(rows))
	for _, row := range rows {
		assignments = append(assignments, api.TemplateTagAssignment{
			AssignmentId:    row.AssignmentID,
			BuildId:         row.BuildID,
			AssignedAt:      row.AssignedAt.Time,
			BuildCreatedAt:  row.BuildCreatedAt,
			BuildFinishedAt: row.BuildFinishedAt,
		})
	}

	var nextCursor *string
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		cursor := fmt.Sprintf("%s|%s", last.AssignedAt.Time.UTC().Format(time.RFC3339Nano), last.AssignmentID.String())
		nextCursor = &cursor
	}

	c.JSON(http.StatusOK, api.TemplateTagAssignmentsResponse{
		Data:       assignments,
		NextCursor: nextCursor,
	})
}

func normalizeTagAssignmentsLimit(limit *api.TagAssignmentsLimit) int32 {
	if limit == nil {
		return defaultTagAssignmentsLimit
	}

	if *limit < 1 {
		return 1
	}

	if *limit > maxTagAssignmentsLimit {
		return maxTagAssignmentsLimit
	}

	return *limit
}

func parseTagAssignmentsCursor(cursor *api.TagAssignmentsCursor) (time.Time, uuid.UUID, error) {
	defaultID := uuid.MustParse(maxCursorID)
	if cursor == nil || *cursor == "" {
		// Sentinel: future timestamp + max UUID returns the newest rows on the first page.
		return time.Now().UTC().Add(time.Hour), defaultID, nil
	}

	parts := strings.SplitN(*cursor, "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, errors.New("invalid cursor format")
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
