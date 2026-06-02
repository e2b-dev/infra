package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
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
	defaultAssignmentsPerGroup = int32(6)
	maxAssignmentsPerGroup     = int32(25)

	defaultTagGroupsLimit = int32(25)
	maxTagGroupsLimit     = int32(100)
	maxTagGroupsSearchLen = 64
)

var (
	tagGroupsSearchRegex = regexp.MustCompile(`^[a-z0-9._-]*$`)

	errInvalidSort           = errors.New("invalid sort")
	errInvalidSearch         = errors.New("invalid search")
	errInvalidCursorFormat   = errors.New("invalid cursor format")
	errCursorSortMismatch    = errors.New("cursor sort mismatch")
	errInvalidCursorTime     = errors.New("invalid cursor time")
	errCursorTagOutOfCharset = errors.New("invalid cursor tag")
)

type tagGroupsSort string

const (
	tagGroupsSortLatestDesc tagGroupsSort = "latest_desc"
	tagGroupsSortLatestAsc  tagGroupsSort = "latest_asc"
	tagGroupsSortNameAsc    tagGroupsSort = "name_asc"
	tagGroupsSortNameDesc   tagGroupsSort = "name_desc"
)

func (s *APIStore) GetTemplatesTemplateIDTagsGroups(c *gin.Context, templateID api.TemplateID, params api.GetTemplatesTemplateIDTagsGroupsParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list template tag groups")

	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()), telemetry.WithTemplateID(templateID))

	if !s.requireTemplateAccess(c, templateID, teamID) {
		return
	}

	sort, err := parseTagGroupsSort(params.Sort)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sort parameter")

		return
	}

	search, err := normalizeTagGroupsSearch(params.Search)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid search parameter")

		return
	}

	cursorTime, cursorTag, err := parseTagGroupsCursor(params.TagsCursor, sort)
	if err != nil {
		switch {
		case errors.Is(err, errCursorSortMismatch):
			s.sendAPIStoreError(c, http.StatusBadRequest, "Cursor was issued for a different sort; clear the cursor and restart")
		default:
			s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid cursor")
		}

		return
	}

	assignmentLimit := normalizeAssignmentsPerGroupLimit(params.AssignmentLimit)
	tagsLimit := normalizeTagGroupsLimit(params.TagsLimit)

	rows, err := s.listTemplateTagGroups(
		ctx,
		sort,
		templateID,
		search,
		cursorTime,
		cursorTag,
		tagsLimit+1,
		assignmentLimit+1,
	)
	if err != nil {
		logger.L().Error(
			ctx,
			"error listing template tag groups",
			zap.Error(err),
			zap.String("sort", string(sort)),
			logger.WithTeamID(teamID.String()),
			logger.WithTemplateID(templateID),
		)
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template tag groups")

		return
	}

	groups, nextCursor := buildTagGroups(rows, sort, assignmentLimit, tagsLimit)

	c.JSON(http.StatusOK, api.TemplateTagGroupsResponse{
		Tags:       groups,
		NextCursor: nextCursor,
	})
}

func (s *APIStore) GetTemplatesTemplateIDTagsCount(c *gin.Context, templateID api.TemplateID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "count template tags")

	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()), telemetry.WithTemplateID(templateID))

	if !s.requireTemplateAccess(c, templateID, teamID) {
		return
	}

	total, err := s.db.Dashboard.CountTemplateTags(ctx, templateID)
	if err != nil {
		logger.L().Error(
			ctx,
			"error counting template tags",
			zap.Error(err),
			logger.WithTeamID(teamID.String()),
			logger.WithTemplateID(templateID),
		)
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when counting template tags")

		return
	}

	c.JSON(http.StatusOK, api.TemplateTagsCountResponse{Total: total})
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
		logger.L().Error(
			ctx,
			"error checking template tag existence",
			zap.Error(err),
			logger.WithTeamID(teamID.String()),
			logger.WithTemplateID(templateID),
		)
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when checking template tag existence")

		return
	}

	c.JSON(http.StatusOK, api.TemplateTagExistsResponse{
		Exists:        exists,
		NormalizedTag: normalizedTag,
	})
}

type tagGroupRow struct {
	AssignmentID     uuid.UUID
	Tag              string
	BuildID          uuid.UUID
	AssignedAt       time.Time
	BuildCreatedAt   time.Time
	BuildFinishedAt  *time.Time
	LatestAssignedAt time.Time
}

func (s *APIStore) listTemplateTagGroups(
	ctx context.Context,
	sort tagGroupsSort,
	templateID api.TemplateID,
	search string,
	cursorTime *time.Time,
	cursorTag *string,
	tagsLimitPlusOne int32,
	assignmentLimitPlusOne int32,
) ([]tagGroupRow, error) {
	switch sort {
	case tagGroupsSortLatestDesc:
		rows, err := s.db.Dashboard.ListTemplateTagGroupsByLatestDesc(ctx, dashboardqueries.ListTemplateTagGroupsByLatestDescParams{
			TemplateID:             templateID,
			Search:                 search,
			CursorTime:             cursorTime,
			CursorTag:              cursorTag,
			TagsLimitPlusOne:       tagsLimitPlusOne,
			AssignmentLimitPlusOne: assignmentLimitPlusOne,
		})
		if err != nil {
			return nil, fmt.Errorf("listing tag groups (latest_desc): %w", err)
		}
		out := make([]tagGroupRow, len(rows))
		for i, r := range rows {
			out[i] = tagGroupRow{
				AssignmentID:     r.AssignmentID,
				Tag:              r.Tag,
				BuildID:          r.BuildID,
				AssignedAt:       r.AssignedAt.Time,
				BuildCreatedAt:   r.BuildCreatedAt,
				BuildFinishedAt:  r.BuildFinishedAt,
				LatestAssignedAt: r.LatestAssignedAt,
			}
		}

		return out, nil

	case tagGroupsSortLatestAsc:
		rows, err := s.db.Dashboard.ListTemplateTagGroupsByLatestAsc(ctx, dashboardqueries.ListTemplateTagGroupsByLatestAscParams{
			TemplateID:             templateID,
			Search:                 search,
			CursorTime:             cursorTime,
			CursorTag:              cursorTag,
			TagsLimitPlusOne:       tagsLimitPlusOne,
			AssignmentLimitPlusOne: assignmentLimitPlusOne,
		})
		if err != nil {
			return nil, fmt.Errorf("listing tag groups (latest_asc): %w", err)
		}
		out := make([]tagGroupRow, len(rows))
		for i, r := range rows {
			out[i] = tagGroupRow{
				AssignmentID:     r.AssignmentID,
				Tag:              r.Tag,
				BuildID:          r.BuildID,
				AssignedAt:       r.AssignedAt.Time,
				BuildCreatedAt:   r.BuildCreatedAt,
				BuildFinishedAt:  r.BuildFinishedAt,
				LatestAssignedAt: r.LatestAssignedAt,
			}
		}

		return out, nil

	case tagGroupsSortNameAsc:
		rows, err := s.db.Dashboard.ListTemplateTagGroupsByNameAsc(ctx, dashboardqueries.ListTemplateTagGroupsByNameAscParams{
			TemplateID:             templateID,
			Search:                 search,
			CursorTag:              cursorTag,
			TagsLimitPlusOne:       tagsLimitPlusOne,
			AssignmentLimitPlusOne: assignmentLimitPlusOne,
		})
		if err != nil {
			return nil, fmt.Errorf("listing tag groups (name_asc): %w", err)
		}
		out := make([]tagGroupRow, len(rows))
		for i, r := range rows {
			out[i] = tagGroupRow{
				AssignmentID:     r.AssignmentID,
				Tag:              r.Tag,
				BuildID:          r.BuildID,
				AssignedAt:       r.AssignedAt.Time,
				BuildCreatedAt:   r.BuildCreatedAt,
				BuildFinishedAt:  r.BuildFinishedAt,
				LatestAssignedAt: r.LatestAssignedAt,
			}
		}

		return out, nil

	case tagGroupsSortNameDesc:
		rows, err := s.db.Dashboard.ListTemplateTagGroupsByNameDesc(ctx, dashboardqueries.ListTemplateTagGroupsByNameDescParams{
			TemplateID:             templateID,
			Search:                 search,
			CursorTag:              cursorTag,
			TagsLimitPlusOne:       tagsLimitPlusOne,
			AssignmentLimitPlusOne: assignmentLimitPlusOne,
		})
		if err != nil {
			return nil, fmt.Errorf("listing tag groups (name_desc): %w", err)
		}
		out := make([]tagGroupRow, len(rows))
		for i, r := range rows {
			out[i] = tagGroupRow{
				AssignmentID:     r.AssignmentID,
				Tag:              r.Tag,
				BuildID:          r.BuildID,
				AssignedAt:       r.AssignedAt.Time,
				BuildCreatedAt:   r.BuildCreatedAt,
				BuildFinishedAt:  r.BuildFinishedAt,
				LatestAssignedAt: r.LatestAssignedAt,
			}
		}

		return out, nil

	default:
		return nil, fmt.Errorf("unsupported sort: %q", sort)
	}
}

// buildTagGroups assembles API groups from the flat row stream, trims any
// (tagsLimit+1)th tag, and returns the nextCursor pointing at the last
// surviving group when one was dropped.
func buildTagGroups(
	rows []tagGroupRow,
	sort tagGroupsSort,
	assignmentLimit int32,
	tagsLimit int32,
) ([]api.TemplateTagGroup, *string) {
	groups := make([]api.TemplateTagGroup, 0)
	groupIndexByTag := map[string]int{}
	latestByTag := map[string]time.Time{}

	for _, row := range rows {
		groupIndex, ok := groupIndexByTag[row.Tag]
		if !ok {
			if int32(len(groups)) >= tagsLimit {
				// (tagsLimit+1)-th tag arrived — skip its rows. We've already
				// captured latest_assigned_at for the previous, surviving tag.
				continue
			}

			groupIndex = len(groups)
			groupIndexByTag[row.Tag] = groupIndex
			latestByTag[row.Tag] = row.LatestAssignedAt
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
			AssignedAt:      row.AssignedAt,
			BuildCreatedAt:  row.BuildCreatedAt,
			BuildFinishedAt: row.BuildFinishedAt,
		})
	}

	var nextCursor *string
	if hasMore := tagsLimitWasExceeded(rows, tagsLimit); hasMore && len(groups) > 0 {
		last := groups[len(groups)-1]
		cursor := formatTagGroupsCursor(sort, latestByTag[last.Tag], last.Tag)
		nextCursor = &cursor
	}

	return groups, nextCursor
}

// tagsLimitWasExceeded reports whether more than `tagsLimit` distinct tags
// appeared in `rows`, signalling a next page exists.
func tagsLimitWasExceeded(rows []tagGroupRow, tagsLimit int32) bool {
	seen := make(map[string]struct{}, tagsLimit+1)
	for _, r := range rows {
		seen[r.Tag] = struct{}{}
		if int32(len(seen)) > tagsLimit {
			return true
		}
	}

	return false
}

func formatTagGroupsCursor(sort tagGroupsSort, latestAt time.Time, tag string) string {
	return fmt.Sprintf("%s|%s|%s", sort, latestAt.UTC().Format(time.RFC3339Nano), tag)
}

func normalizeAssignmentsPerGroupLimit(limit *api.TagAssignmentLimit) int32 {
	if limit == nil {
		return defaultAssignmentsPerGroup
	}

	if *limit < 1 {
		return 1
	}

	if *limit > maxAssignmentsPerGroup {
		return maxAssignmentsPerGroup
	}

	return *limit
}

func normalizeTagGroupsLimit(limit *api.TagGroupsLimit) int32 {
	if limit == nil {
		return defaultTagGroupsLimit
	}

	if *limit < 1 {
		return 1
	}

	if *limit > maxTagGroupsLimit {
		return maxTagGroupsLimit
	}

	return *limit
}

func parseTagGroupsSort(value *api.GetTemplatesTemplateIDTagsGroupsParamsSort) (tagGroupsSort, error) {
	if value == nil {
		return tagGroupsSortLatestDesc, nil
	}

	switch tagGroupsSort(*value) {
	case tagGroupsSortLatestDesc, tagGroupsSortLatestAsc, tagGroupsSortNameAsc, tagGroupsSortNameDesc:
		return tagGroupsSort(*value), nil
	default:
		return "", fmt.Errorf("%w: %q", errInvalidSort, *value)
	}
}

func normalizeTagGroupsSearch(value *api.TagGroupsSearch) (string, error) {
	if value == nil {
		return "", nil
	}

	cleaned := strings.ToLower(strings.TrimSpace(*value))
	if cleaned == "" {
		return "", nil
	}

	if len(cleaned) > maxTagGroupsSearchLen {
		return "", fmt.Errorf("%w: too long", errInvalidSearch)
	}

	if !tagGroupsSearchRegex.MatchString(cleaned) {
		return "", fmt.Errorf("%w: charset", errInvalidSearch)
	}

	return cleaned, nil
}

func parseTagGroupsCursor(cursor *api.TagGroupsCursor, sort tagGroupsSort) (*time.Time, *string, error) {
	if cursor == nil || *cursor == "" {
		return nil, nil, nil
	}

	parts := strings.SplitN(*cursor, "|", 3)
	if len(parts) != 3 {
		return nil, nil, errInvalidCursorFormat
	}

	if parts[0] != string(sort) {
		return nil, nil, errCursorSortMismatch
	}

	cursorTime, err := parseCursorTime(parts[1])
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errInvalidCursorTime, err)
	}

	// Reject malformed tag payloads (embedded pipes already excluded by
	// SplitN). The empty string is rejected too — a real next-page cursor
	// always pins a concrete tag.
	if !tagGroupsSearchRegex.MatchString(parts[2]) || parts[2] == "" {
		return nil, nil, errCursorTagOutOfCharset
	}

	cursorTag := parts[2]

	return &cursorTime, &cursorTag, nil
}
