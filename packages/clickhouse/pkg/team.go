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
	SandboxStartedRate  float64   `ch:"started_sandboxes_rate"`
	ConcurrentSandboxes int64     `ch:"concurrent_sandboxes"`
}

const teamMetricsSelectQuery = `
WITH
  created AS (
    SELECT
      toStartOfInterval(timestamp, interval {step:UInt32} second) AS ts,
      sum(value) as created_sandboxes
    FROM team_metrics_sum
    WHERE metric_name = 'e2b.team.sandbox.created'
      AND team_id = {team_id:String}
      AND timestamp BETWEEN {start_time:DateTime64} AND {end_time:DateTime64}
	GROUP BY ts
  ),
  concurrent AS (
    SELECT
      toStartOfInterval(timestamp, interval {step:UInt32} second) AS ts,
      toInt64(max(value)) AS concurrent_sandboxes
    FROM team_metrics_gauge
    WHERE metric_name = 'e2b.team.sandbox.max_concurrent'
      AND team_id = {team_id:String}
      AND timestamp BETWEEN {start_time:DateTime64} AND {end_time:DateTime64}
	GROUP BY ts
  ),
  all_ts AS (
    SELECT ts FROM created
    UNION DISTINCT
    SELECT ts FROM concurrent
  )
SELECT
  all_ts.ts AS ts,
  COALESCE(created_sandboxes / {step:Float64}, 0.0) AS started_sandboxes_rate,
  COALESCE(concurrent_sandboxes, 0)                 AS concurrent_sandboxes
FROM all_ts
LEFT JOIN created cr      ON cr.ts = all_ts.ts
LEFT JOIN concurrent con ON con.ts = all_ts.ts
ORDER BY all_ts.ts ASC;
`

func (c *Client) QueryTeamMetrics(ctx context.Context, teamID string, start time.Time, end time.Time, step time.Duration) ([]TeamMetrics, error) {
	rows, err := c.conn.Query(ctx, teamMetricsSelectQuery,
		clickhouse.Named("team_id", teamID),
		clickhouse.DateNamed("start_time", start, clickhouse.Seconds),
		clickhouse.DateNamed("end_time", end, clickhouse.Seconds),
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
