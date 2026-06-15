package handlers

import (
	"net/http"
	"strings"

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
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	defaultBuildsLimit = int32(50)
	maxBuildsLimit     = int32(100)
)

func (s *APIStore) GetBuilds(c *gin.Context, params api.GetBuildsParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list builds")

	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))

	limit := normalizeBuildsLimit(params.Limit)

	cursorCreatedAt, cursorID, err := parseBuildsCursor(params.Cursor)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid cursor")

		return
	}

	statusGroups := dashboardutils.MapBuildStatusesToDBStatusGroups(params.Statuses)

	queryParams := queries.GetTeamBuildsPageParams{
		TeamID:          teamID,
		Statuses:        buildStatusGroupsToStrings(statusGroups),
		CpuCount:        int64(utils.DerefOrDefault(params.CpuCount, 0)),
		MemoryMb:        int64(utils.DerefOrDefault(params.MemoryMB, 0)),
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
		LimitPlusOne:    limit + 1,
	}
	// A UUID search term matches a build id exactly; anything else is a template
	// id (exact) or template name/alias (substring) search.
	if search := strings.TrimSpace(utils.DerefOrDefault(params.BuildIdOrTemplate, "")); search != "" {
		if buildID, parseErr := uuid.Parse(search); parseErr == nil {
			queryParams.FilterBuildID = &buildID
		} else {
			queryParams.NameSearch = &search
		}
	}

	rows, err := s.db.GetTeamBuildsPage(ctx, queryParams)
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
			Status:        dashboardutils.MapBuildStatusFromDBStatusGroup(row.StatusGroup),
			StatusMessage: dashboardutils.MapBuildStatusMessageFromDBStatusGroup(row.StatusGroup, row.Reason),
			CreatedAt:     row.CreatedAt,
			FinishedAt:    row.FinishedAt,
			CpuCount:      row.Vcpu,
			MemoryMB:      row.RamMb,
			DiskSizeMB:    row.TotalDiskSizeMb,
			EnvdVersion:   row.EnvdVersion,
		})
	}

	var nextCursor *string
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		cursor := formatBuildsCursor(last.CreatedAt, last.ID.String())
		nextCursor = &cursor
	}

	c.JSON(http.StatusOK, api.BuildsListResponse{
		Data:       builds,
		NextCursor: nextCursor,
	})
}

func buildStatusGroupsToStrings(groups []dbtypes.BuildStatusGroup) []string {
	statuses := make([]string, 0, len(groups))
	for _, group := range groups {
		statuses = append(statuses, string(group))
	}

	return statuses
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

	return *limit
}
