package handlers

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

var errInvalidBuildsCursor = errors.New("invalid cursor")

// parseBuildsCursor parses a `{createdAt}|{buildID}` cursor. nil/nil is the first
// page.
func parseBuildsCursor(cursor *api.BuildsCursor) (*time.Time, *uuid.UUID, error) {
	if cursor == nil || *cursor == "" {
		return nil, nil, nil
	}

	parts := strings.SplitN(*cursor, "|", 2)
	if len(parts) != 2 {
		return nil, nil, errInvalidBuildsCursor
	}

	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errInvalidBuildsCursor, err)
	}

	id, err := uuid.Parse(parts[1])
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errInvalidBuildsCursor, err)
	}

	return &createdAt, &id, nil
}

func formatBuildsCursor(createdAt time.Time, id string) string {
	return fmt.Sprintf("%s|%s", createdAt.UTC().Format(time.RFC3339Nano), id)
}
