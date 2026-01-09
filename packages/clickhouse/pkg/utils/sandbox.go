package utils

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func GetSandboxStartEndTime(ctx context.Context, clickhouseStore clickhouse.SandboxQueriesProvider, teamID, sandboxID string, qStart *int64, qEnd *int64) (time.Time, time.Time, error) {
	// Check if the sandbox exists
	var start, end time.Time
	if qStart != nil {
		start = time.Unix(*qStart, 0)
	}

	if qEnd != nil {
		end = time.Unix(*qEnd, 0)
	}

	if start.IsZero() || end.IsZero() {
		sbxStart, sbxEnd, err := clickhouseStore.QuerySandboxTimeRange(ctx, sandboxID, teamID)
		if err != nil {
			logger.L().Error(ctx, "Error fetching sandbox time range from ClickHouse",
				logger.WithSandboxID(sandboxID),
				logger.WithTeamID(teamID),
				zap.Error(err),
			)

			return time.Time{}, time.Time{}, fmt.Errorf("error querying sandbox time range: %w", err)
		}

		if start.IsZero() {
			start = sbxStart
		}

		if end.IsZero() {
			end = sbxEnd
		}
	}

	return start, end, nil
}
