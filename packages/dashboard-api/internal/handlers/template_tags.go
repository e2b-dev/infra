package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	defaultTagAssignmentLimit = int32(6)
	maxTagAssignmentLimit     = int32(25)
)

func (s *APIStore) GetTemplatesTemplateIDTagsGroups(c *gin.Context, templateID api.TemplateID, params api.GetTemplatesTemplateIDTagsGroupsParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list template tag groups")

	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()), telemetry.WithTemplateID(templateID))

	if !s.requireTemplateAccess(c, templateID, teamID) {
		return
	}

	assignmentLimit := normalizeTagAssignmentLimit(params.AssignmentLimit)
	rows, err := s.db.Dashboard.ListTemplateTagGroupAssignments(ctx, dashboardqueries.ListTemplateTagGroupAssignmentsParams{
		TemplateID:             templateID,
		AssignmentLimitPlusOne: assignmentLimit + 1,
	})
	if err != nil {
		logger.L().Error(ctx, "Error getting template tag groups", zap.Error(err), logger.WithTeamID(teamID.String()), logger.WithTemplateID(templateID))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template tag groups")

		return
	}

	groups := make([]api.TemplateTagGroup, 0)
	groupIndexByTag := map[string]int{}

	for _, row := range rows {
		groupIndex, ok := groupIndexByTag[row.Tag]
		if !ok {
			groupIndex = len(groups)
			groupIndexByTag[row.Tag] = groupIndex
			groups = append(groups, api.TemplateTagGroup{
				Tag:         row.Tag,
				Assignments: []api.TemplateTagAssignment{},
			})
		}

		if int32(len(groups[groupIndex].Assignments)) >= assignmentLimit {
			groups[groupIndex].HasMore = true

			continue
		}

		groups[groupIndex].Assignments = append(groups[groupIndex].Assignments, api.TemplateTagAssignment{
			AssignmentId:    row.AssignmentID,
			BuildId:         row.BuildID,
			AssignedAt:      row.AssignedAt.Time,
			BuildCreatedAt:  row.BuildCreatedAt,
			BuildFinishedAt: row.BuildFinishedAt,
		})
	}

	c.JSON(http.StatusOK, api.TemplateTagGroupsResponse{
		Tags: groups,
	})
}

func (s *APIStore) GetTemplatesTemplateIDTagsExists(c *gin.Context, templateID api.TemplateID, params api.GetTemplatesTemplateIDTagsExistsParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "check template tag exists")

	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()), telemetry.WithTemplateID(templateID))

	if !s.requireTemplateAccess(c, templateID, teamID) {
		return
	}

	tags, err := id.ValidateAndDeduplicateTags([]string{params.Tag})
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid tag")

		return
	}

	normalizedTag := tags[0]
	exists, err := s.db.Dashboard.CheckReadyTemplateTagExists(ctx, dashboardqueries.CheckReadyTemplateTagExistsParams{
		TemplateID: templateID,
		Tag:        normalizedTag,
	})
	if err != nil {
		logger.L().Error(ctx, "Error checking template tag existence", zap.Error(err), logger.WithTeamID(teamID.String()), logger.WithTemplateID(templateID))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when checking template tag existence")

		return
	}

	c.JSON(http.StatusOK, api.TemplateTagExistsResponse{
		Exists:        exists,
		NormalizedTag: normalizedTag,
	})
}

func normalizeTagAssignmentLimit(limit *api.TagAssignmentLimit) int32 {
	if limit == nil {
		return defaultTagAssignmentLimit
	}

	if *limit < 1 {
		return 1
	}

	if *limit > maxTagAssignmentLimit {
		return maxTagAssignmentLimit
	}

	return *limit
}
