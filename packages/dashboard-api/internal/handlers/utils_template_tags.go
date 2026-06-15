package handlers

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
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

func normalizeAssignmentsPerGroupLimit(limit *api.TagAssignmentLimit) int32 {
	v := utils.DerefOrDefault(limit, defaultAssignmentsPerGroup)
	if v < 1 {
		return 1
	}

	if v > maxAssignmentsPerGroup {
		return maxAssignmentsPerGroup
	}

	return v
}

func normalizeTagGroupsLimit(limit *api.TagGroupsLimit) int32 {
	v := utils.DerefOrDefault(limit, defaultTagGroupsLimit)
	if v < 1 {
		return 1
	}

	if v > maxTagGroupsLimit {
		return maxTagGroupsLimit
	}

	return v
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
	cleaned := strings.ToLower(strings.TrimSpace(utils.DerefOrDefault(value, "")))
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

func formatTagGroupsCursor(sort tagGroupsSort, latestAt time.Time, tag string) string {
	return fmt.Sprintf("%s|%s|%s", sort, latestAt.UTC().Format(time.RFC3339Nano), tag)
}
