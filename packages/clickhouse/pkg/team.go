package clickhouse

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

type TeamMetrics struct {
	Timestamp           time.Time `ch:"ts"`
	StartedSandboxes    int64     `ch:"started_sandboxes"`
	ConcurrentSandboxes int64     `ch:"concurrent_sandboxes"`
}

const teamMetricsSelectQuery = `
SELECT   toStartOfInterval(g.timestamp, interval {step:UInt32} second) AS ts,
         toInt64(maxIf(s.value, s.metric_name = 'e2b.team.sandbox.started'))          AS started_sandboxes,
         toInt64(maxIf(g.value, g.metric_name = 'e2b.team.sandbox.max')) AS concurrent_sandboxes
FROM     team_metrics_gauge g
JOIN     team_metrics_sum s ON toStartOfInterval(g.timestamp, interval {step:UInt32} second) = toStartOfInterval(s.timestamp, interval {step:UInt32} second)
WHERE    team_id = {team_id:String}
AND      timestamp >= {start_time:DateTime64}
AND      timestamp <= {end_time:DateTime64}
GROUP BY ts
ORDER BY ts;
`

func (c *Client) QueryTeamMetrics(ctx context.Context, teamID string, start time.Time, end time.Time, step time.Duration) ([]TeamMetrics, error) {
	rows, err := c.conn.Query(ctx, teamMetricsSelectQuery,
		clickhouse.Named("team_id", teamID),
		clickhouse.DateNamed("start_time", start, clickhouse.Seconds),
		// Add an extra second to include the end time in the range
		clickhouse.DateNamed("end_time", end.Add(time.Second), clickhouse.Seconds),
		clickhouse.Named("step", strconv.Itoa(int(step.Seconds()))),
	)
	if err != nil {
		return nil, fmt.Errorf("query team metrics: %w", err)
	}

	defer rows.Close()
	var out []TeamMetrics
	for rows.Next() {
		var m TeamMetrics
		if err := rows.ScanStruct(&m); err != nil {
			return nil, fmt.Errorf("error scanning team metrics: %w", err)
		}
		out = append(out, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over team metrics rows: %w", err)
	}

	return out, nil
}
