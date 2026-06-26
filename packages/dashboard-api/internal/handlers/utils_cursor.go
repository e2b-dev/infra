package handlers

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// maxCursorTime is a far-future sentinel used as the upper bound for the first
// page of any DESC cursor. Using a constant rather than time.Now() avoids
// clock-skew between the app and DB silently dropping newly-inserted rows from
// page 1 (and therefore from every subsequent page).
var (
	maxCursorTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	minCursorTime = time.Time{}
)

// errInvalidCursor is the shared sentinel for malformed cursor payloads.
// Feature handlers errors.Is-check this to produce a 400.
var errInvalidCursor = errors.New("invalid cursor")

// cursorTime is the *string variant returning nil when v is nil.
// It defers to parseCursorTime (defined in builds_list.go) for the actual parse.
func cursorTime(v *string) (*time.Time, error) {
	if v == nil {
		return nil, nil
	}

	t, err := parseCursorTime(*v)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidCursor, err)
	}

	return &t, nil
}

// cursorInt64 parses an int64 cursor segment.
func cursorInt64(v *string) (*int64, error) {
	if v == nil {
		return nil, nil
	}

	n, err := strconv.ParseInt(*v, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidCursor, err)
	}

	return &n, nil
}
