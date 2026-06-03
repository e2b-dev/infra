package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
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
		cpuCount:        templatesInt64(params.CpuCount),
		memoryMb:        templatesInt64(params.MemoryMB),
		filterPublic:    templatesPublicFilter(params.Public),
		search:          templatesSearch(params.Search),
	}

	rows, err := s.listTemplates(ctx, sort, filter, cursorValue, cursorID, limit+1)
	if err != nil {
		if errors.Is(err, errInvalidTemplatesCursor) {
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
	cpuCount        int64
	memoryMb        int64
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
	case templatesSortNameAsc:
		rows, err := s.db.Dashboard.ListTeamTemplatesByNameAsc(ctx, dashboardqueries.ListTeamTemplatesByNameAscParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			CpuCount:        filter.cpuCount,
			MemoryMb:        filter.memoryMb,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorName:      cursorValue,
			CursorID:        cursorID,
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
	case templatesSortNameDesc:
		rows, err := s.db.Dashboard.ListTeamTemplatesByNameDesc(ctx, dashboardqueries.ListTeamTemplatesByNameDescParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			CpuCount:        filter.cpuCount,
			MemoryMb:        filter.memoryMb,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorName:      cursorValue,
			CursorID:        cursorID,
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
	case templatesSortCpuCountAsc:
		cursorCPU, parseErr := cursorInt64(cursorValue)
		if parseErr != nil {
			return nil, parseErr
		}
		rows, err := s.db.Dashboard.ListTeamTemplatesByCpuCountAsc(ctx, dashboardqueries.ListTeamTemplatesByCpuCountAscParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			CpuCount:        filter.cpuCount,
			MemoryMb:        filter.memoryMb,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorCpuCount:  cursorCPU,
			CursorID:        cursorID,
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
	case templatesSortCpuCountDesc:
		cursorCPU, parseErr := cursorInt64(cursorValue)
		if parseErr != nil {
			return nil, parseErr
		}
		rows, err := s.db.Dashboard.ListTeamTemplatesByCpuCountDesc(ctx, dashboardqueries.ListTeamTemplatesByCpuCountDescParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			CpuCount:        filter.cpuCount,
			MemoryMb:        filter.memoryMb,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorCpuCount:  cursorCPU,
			CursorID:        cursorID,
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
	case templatesSortMemoryMbAsc:
		cursorMem, parseErr := cursorInt64(cursorValue)
		if parseErr != nil {
			return nil, parseErr
		}
		rows, err := s.db.Dashboard.ListTeamTemplatesByMemoryMbAsc(ctx, dashboardqueries.ListTeamTemplatesByMemoryMbAscParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			CpuCount:        filter.cpuCount,
			MemoryMb:        filter.memoryMb,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorMemoryMb:  cursorMem,
			CursorID:        cursorID,
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
	case templatesSortMemoryMbDesc:
		cursorMem, parseErr := cursorInt64(cursorValue)
		if parseErr != nil {
			return nil, parseErr
		}
		rows, err := s.db.Dashboard.ListTeamTemplatesByMemoryMbDesc(ctx, dashboardqueries.ListTeamTemplatesByMemoryMbDescParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			CpuCount:        filter.cpuCount,
			MemoryMb:        filter.memoryMb,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorMemoryMb:  cursorMem,
			CursorID:        cursorID,
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
	case templatesSortCreatedAtAsc:
		cursorTs, parseErr := cursorTime(cursorValue)
		if parseErr != nil {
			return nil, parseErr
		}
		rows, err := s.db.Dashboard.ListTeamTemplatesByCreatedAtAsc(ctx, dashboardqueries.ListTeamTemplatesByCreatedAtAscParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			CpuCount:        filter.cpuCount,
			MemoryMb:        filter.memoryMb,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorCreatedAt: cursorTs,
			CursorID:        cursorID,
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
		rows, err := s.db.Dashboard.ListTeamTemplatesByCreatedAtDesc(ctx, dashboardqueries.ListTeamTemplatesByCreatedAtDescParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			CpuCount:        filter.cpuCount,
			MemoryMb:        filter.memoryMb,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorCreatedAt: cursorTs,
			CursorID:        cursorID,
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
		rows, err := s.db.Dashboard.ListTeamTemplatesByUpdatedAtAsc(ctx, dashboardqueries.ListTeamTemplatesByUpdatedAtAscParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			CpuCount:        filter.cpuCount,
			MemoryMb:        filter.memoryMb,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorUpdatedAt: cursorTs,
			CursorID:        cursorID,
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
		rows, err := s.db.Dashboard.ListTeamTemplatesByUpdatedAtDesc(ctx, dashboardqueries.ListTeamTemplatesByUpdatedAtDescParams{
			TeamID:          filter.teamID,
			IncludeDefaults: filter.includeDefaults,
			CpuCount:        filter.cpuCount,
			MemoryMb:        filter.memoryMb,
			FilterPublic:    filter.filterPublic,
			Search:          filter.search,
			CursorUpdatedAt: cursorTs,
			CursorID:        cursorID,
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

func templatesInt64(v *int32) int64 {
	if v == nil {
		return 0
	}

	return int64(*v)
}

func templatesSearch(v *api.TemplatesSearch) string {
	if v == nil {
		return ""
	}

	return strings.TrimSpace(*v)
}

// templatesSortValue serializes the value of the chosen sort column for the
// given row so it can be embedded in the next-page cursor.
func templatesSortValue(sort templatesSort, f templateRowFields) string {
	switch sort {
	case templatesSortNameAsc, templatesSortNameDesc:
		return f.NameSortKey
	case templatesSortCpuCountAsc, templatesSortCpuCountDesc:
		return strconv.FormatInt(f.CpuCount, 10)
	case templatesSortMemoryMbAsc, templatesSortMemoryMbDesc:
		return strconv.FormatInt(f.MemoryMb, 10)
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
		// Email is intentionally left unset while the Supabase auth migration is
		// in progress; we only expose the creator id for now.
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
