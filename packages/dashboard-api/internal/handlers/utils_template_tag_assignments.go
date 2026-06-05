package handlers

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

const (
	defaultTagAssignmentsPageSize = int32(50)
	maxTagAssignmentsPageSize     = int32(100)
)

func normalizeTagAssignmentsPageLimit(limit *api.TagAssignmentsLimit) int32 {
	if limit == nil {
		return defaultTagAssignmentsPageSize
	}

	if *limit < 1 {
		return 1
	}

	if *limit > maxTagAssignmentsPageSize {
		return maxTagAssignmentsPageSize
	}

	return *limit
}

// parseTagAssignmentsCursor decodes the {assigned_at}|{assignment_id} cursor.
// An empty cursor is the first page; we use maxCursorTime + maxCursorID as the
// upper-bound sentinel so DB rows inserted concurrently with the request can
// still appear on page 1 regardless of app-vs-DB clock skew.
func parseTagAssignmentsCursor(cursor *api.TagAssignmentsCursor) (time.Time, uuid.UUID, error) {
	defaultID := uuid.MustParse(maxCursorID)
	if cursor == nil || *cursor == "" {
		return maxCursorTime, defaultID, nil
	}

	parts := strings.SplitN(*cursor, "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: bad format", errInvalidCursor)
	}

	ts, err := parseCursorTime(parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: %w", errInvalidCursor, err)
	}

	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: %w", errInvalidCursor, err)
	}

	return ts, id, nil
}
