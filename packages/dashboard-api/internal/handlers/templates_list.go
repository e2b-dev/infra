package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (s *APIStore) GetTemplates(c *gin.Context, params api.GetTemplatesParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list templates")

	teamID := auth.MustGetTeamID(c)
	team := auth.MustGetTeamInfo(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))

	limit := normalizeTemplatesLimit(params.Limit)

	sort, err := parseTemplatesSort(params.Sort)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sort parameter")

		return
	}

	cursorValue, cursorID, err := parseTemplatesCursor(params.Cursor, sort)
	if err != nil {
		if errors.Is(err, errTemplatesCursorSortMismatch) {
			s.sendAPIStoreError(c, http.StatusBadRequest, "Cursor was issued for a different sort; clear the cursor and restart")
		} else {
			s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid cursor")
		}

		return
	}

	filter := templatesFilter{
		teamID: teamID,
		// Default templates are E2B-wide and don't belong on a dedicated cluster.
		includeDefaults: team.ClusterID == nil,
		filterPublic:    templatesPublicFilter(params.Public),
		search:          strings.TrimSpace(utils.DerefOrDefault(params.Search, "")),
	}

	rows, err := s.listTemplates(ctx, sort, filter, cursorValue, cursorID, limit+1)
	if err != nil {
		if errors.Is(err, errInvalidCursor) || errors.Is(err, errInvalidTemplatesCursor) {
			s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid cursor")

			return
		}

		logger.L().Error(ctx, "Error getting templates", zap.Error(err), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting templates")

		return
	}

	hasMore := len(rows) > int(limit)
	if hasMore {
		rows = rows[:limit]
	}

	templates := make([]api.TeamTemplate, 0, len(rows))
	for _, row := range rows {
		templates = append(templates, row.toAPI())
	}

	var nextCursor *string
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		cursor := formatTemplatesCursor(sort, templatesSortValue(sort, last), last.TemplateID)
		nextCursor = &cursor
	}

	c.JSON(http.StatusOK, api.TeamTemplatesResponse{
		Data:       templates,
		NextCursor: nextCursor,
	})
}

type templatesFilter struct {
	teamID          uuid.UUID
	includeDefaults bool
	filterPublic    int16
	search          string
}

// listTemplates selects the query matching the requested sort, parses the
// (string) cursor value into that sort's typed parameter, runs it, and returns
// the rows in the shared projection shape.
func (s *APIStore) listTemplates(
	ctx context.Context,
	sort templatesSort,
	filter templatesFilter,
	cursorValue *string,
	cursorID *string,
	limitPlusOne int32,
) ([]templateRowFields, error) {
	switch sort {
	case templatesSortCreatedAtAsc:
		cursorTs, parseErr := cursorTime(cursorValue)
		if parseErr != nil {
			return nil, parseErr
		}
		ct, cid := timeCursor(cursorTs, cursorID, false)
		rows, err := s.db.Dashboard.ListTeamTemplatesByCreatedAtAsc(ctx, dashboardqueries.ListTeamTemplatesByCreatedAtAscParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorCreatedAt: ct,
			CursorID:        cid,
			LimitPlusOne:    limitPlusOne,
		})
		if err != nil {
			return nil, err
		}

		fields := make([]templateRowFields, len(rows))
		for i := range rows {
			fields[i] = templateRowFields(rows[i])
		}

		return fields, nil
	case templatesSortCreatedAtDesc:
		cursorTs, parseErr := cursorTime(cursorValue)
		if parseErr != nil {
			return nil, parseErr
		}
		ct, cid := timeCursor(cursorTs, cursorID, true)
		rows, err := s.db.Dashboard.ListTeamTemplatesByCreatedAtDesc(ctx, dashboardqueries.ListTeamTemplatesByCreatedAtDescParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorCreatedAt: ct,
			CursorID:        cid,
			LimitPlusOne:    limitPlusOne,
		})
		if err != nil {
			return nil, err
		}

		fields := make([]templateRowFields, len(rows))
		for i := range rows {
			fields[i] = templateRowFields(rows[i])
		}

		return fields, nil
	case templatesSortUpdatedAtAsc:
		cursorTs, parseErr := cursorTime(cursorValue)
		if parseErr != nil {
			return nil, parseErr
		}
		ct, cid := timeCursor(cursorTs, cursorID, false)
		rows, err := s.db.Dashboard.ListTeamTemplatesByUpdatedAtAsc(ctx, dashboardqueries.ListTeamTemplatesByUpdatedAtAscParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorUpdatedAt: ct,
			CursorID:        cid,
			LimitPlusOne:    limitPlusOne,
		})
		if err != nil {
			return nil, err
		}

		fields := make([]templateRowFields, len(rows))
		for i := range rows {
			fields[i] = templateRowFields(rows[i])
		}

		return fields, nil
	case templatesSortUpdatedAtDesc:
		cursorTs, parseErr := cursorTime(cursorValue)
		if parseErr != nil {
			return nil, parseErr
		}
		ct, cid := timeCursor(cursorTs, cursorID, true)
		rows, err := s.db.Dashboard.ListTeamTemplatesByUpdatedAtDesc(ctx, dashboardqueries.ListTeamTemplatesByUpdatedAtDescParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorUpdatedAt: ct,
			CursorID:        cid,
			LimitPlusOne:    limitPlusOne,
		})
		if err != nil {
			return nil, err
		}

		fields := make([]templateRowFields, len(rows))
		for i := range rows {
			fields[i] = templateRowFields(rows[i])
		}

		return fields, nil
	default:
		return nil, fmt.Errorf("unsupported sort: %q", sort)
	}
}

// templatesSortValue serializes the value of the chosen sort column for the
// given row so it can be embedded in the next-page cursor.
func templatesSortValue(sort templatesSort, f templateRowFields) string {
	switch sort {
	case templatesSortCreatedAtAsc, templatesSortCreatedAtDesc:
		return f.CreatedAt.UTC().Format(time.RFC3339Nano)
	default: // updated_at
		return f.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
}

// templateRowFields mirrors the shared projection of every ListTeamTemplatesBy*
// row and GetTeamTemplateRow (identical field set/order), so each can be
// converted to it and mapped to the API type through a single code path.
type templateRowFields struct {
	TemplateID         string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Public             bool
	BuildCount         int32
	SpawnCount         int64
	LastSpawnedAt      *time.Time
	CreatorID          *uuid.UUID
	BuildID            uuid.UUID
	CpuCount           int64
	MemoryMb           int64
	DiskSizeMb         *int64
	EnvdVersion        *string
	Aliases            []string
	Names              []string
	IsDefault          bool
	DefaultDescription *string
	NameSortKey        string
}

func (f templateRowFields) toAPI() api.TeamTemplate {
	var createdBy *struct {
		Email *string            `json:"email,omitempty"`
		Id    openapi_types.UUID `json:"id"`
	}
	if f.CreatorID != nil {
		createdBy = &struct {
			Email *string            `json:"email,omitempty"`
			Id    openapi_types.UUID `json:"id"`
		}{
			Id: *f.CreatorID,
		}
	}

	aliases := f.Aliases
	if aliases == nil {
		aliases = []string{}
	}
	names := f.Names
	if names == nil {
		names = []string{}
	}

	return api.TeamTemplate{
		TemplateID:         f.TemplateID,
		BuildID:            f.BuildID,
		CpuCount:           f.CpuCount,
		MemoryMB:           f.MemoryMb,
		DiskSizeMB:         f.DiskSizeMb,
		Public:             f.Public,
		Aliases:            aliases,
		Names:              names,
		CreatedAt:          f.CreatedAt,
		UpdatedAt:          f.UpdatedAt,
		CreatedBy:          createdBy,
		LastSpawnedAt:      f.LastSpawnedAt,
		SpawnCount:         f.SpawnCount,
		BuildCount:         f.BuildCount,
		EnvdVersion:        f.EnvdVersion,
		IsDefault:          f.IsDefault,
		DefaultDescription: f.DefaultDescription,
	}
}
