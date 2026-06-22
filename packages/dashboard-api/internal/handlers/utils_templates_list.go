package handlers

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// templatesSort is the combined sort column + direction. Its values match the
// API `sort` enum and the suffix of the corresponding ListTeamTemplatesBy*
// query, so the handler can select the right query by switching on it.
type templatesSort string

const (
	templatesSortCreatedAtAsc  templatesSort = "created_at_asc"
	templatesSortCreatedAtDesc templatesSort = "created_at_desc"
	templatesSortUpdatedAtAsc  templatesSort = "updated_at_asc"
	templatesSortUpdatedAtDesc templatesSort = "updated_at_desc"
)

const (
	defaultTemplatesSort  = templatesSortCreatedAtDesc
	defaultTemplatesLimit = int32(50)
	maxTemplatesLimit     = int32(100)
)

var (
	errInvalidTemplatesCursor      = errors.New("invalid cursor")
	errTemplatesCursorSortMismatch = errors.New("cursor sort mismatch")
)

func parseTemplatesSort(value *api.GetTemplatesParamsSort) (templatesSort, error) {
	if value == nil {
		return defaultTemplatesSort, nil
	}

	switch templatesSort(*value) {
	case templatesSortCreatedAtAsc, templatesSortCreatedAtDesc,
		templatesSortUpdatedAtAsc, templatesSortUpdatedAtDesc:
		return templatesSort(*value), nil
	default:
		return "", fmt.Errorf("invalid sort: %q", *value)
	}
}

func normalizeTemplatesLimit(limit *api.TemplatesLimit) int32 {
	v := utils.DerefOrDefault(limit, defaultTemplatesLimit)
	if v < 1 {
		return 1
	}
	if v > maxTemplatesLimit {
		return maxTemplatesLimit
	}

	return v
}

// templatesPublicFilter encodes the optional visibility filter for the query:
// -1 means "no filter", 1 means public-only, 0 means internal-only.
func templatesPublicFilter(public *api.TemplatesPublic) int16 {
	if public == nil {
		return -1
	}
	if *public {
		return 1
	}

	return 0
}

// parseTemplatesCursor parses a `{sort}|{value}|{id}` cursor and verifies that
// it was issued for the same sort as the current request. It returns nil
// value/id for an empty cursor (the first page). The value segment stays a
// string here; it is parsed into the typed query parameter when the sort-
// specific query is selected.
func parseTemplatesCursor(cursor *api.TemplatesCursor, sort templatesSort) (*string, *string, error) {
	if cursor == nil || *cursor == "" {
		return nil, nil, nil
	}

	parts := strings.SplitN(*cursor, "|", 3)
	if len(parts) != 3 {
		return nil, nil, errInvalidTemplatesCursor
	}

	if parts[0] != string(sort) {
		return nil, nil, errTemplatesCursorSortMismatch
	}

	// A real next-page cursor always pins a concrete template id.
	if parts[2] == "" {
		return nil, nil, errInvalidTemplatesCursor
	}

	value, id := parts[1], parts[2]

	return &value, &id, nil
}

func formatTemplatesCursor(sort templatesSort, value, id string) string {
	return fmt.Sprintf("%s|%s|%s", sort, value, id)
}

func timeCursor(ts *time.Time, id *string, desc bool) (time.Time, string) {
	if ts != nil && id != nil {
		return *ts, *id
	}
	if desc {
		return maxCursorTime, ""
	}

	return minCursorTime, ""
}
