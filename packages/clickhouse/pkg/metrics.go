package clickhouse

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

type Metrics struct {
	SandboxID      string    `ch:"sandbox_id"`
	TeamID         string    `ch:"team_id"`
	Timestamp      time.Time `ch:"timestamp"`
	CPUCount       float64   `ch:"cpu_total"`
	CPUUsedPercent float64   `ch:"cpu_used"`
	MemTotal       float64   `ch:"ram_total"`
	MemUsed        float64   `ch:"ram_used"`
}

const latestMetricsSelectQuery = `
SELECT
    Attributes['sandbox_id']                                            AS sandbox_id,
    Attributes['team_id']                                               AS team_id,

    argMaxIf(Value,  TimeUnix, MetricName = 'e2b.sandbox.cpu.total')    AS cpu_total,
    argMaxIf(Value,  TimeUnix, MetricName = 'e2b.sandbox.cpu.used')     AS cpu_used,
    argMaxIf(Value,  TimeUnix, MetricName = 'e2b.sandbox.ram.total')    AS ram_total,
    argMaxIf(Value,  TimeUnix, MetricName = 'e2b.sandbox.ram.used')     AS ram_used
FROM metrics_gauge
WHERE 
    Attributes['sandbox_id'] IN ?
AND Attributes['team_id'] = ?
AND MetricName IN (
	  'e2b.sandbox.cpu.total',
	  'e2b.sandbox.cpu.used',
	  'e2b.sandbox.ram.total',
	  'e2b.sandbox.ram.used'
   )
GROUP BY sandbox_id, team_id
`

// QueryLatestMetrics returns rows ordered by timestamp, paged by limit.
func (c *Client) QueryLatestMetrics(ctx context.Context, sandboxIDs []string, teamID string) ([]Metrics, error) {
	if len(sandboxIDs) == 0 {
		return make([]Metrics, 0), nil
	}

	rows, err := c.conn.Query(ctx, latestMetricsSelectQuery,
		sandboxIDs,
		teamID,
	)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	defer rows.Close()

	var out []Metrics
	for rows.Next() {
		var m Metrics
		if err := rows.ScanStruct(&m); err != nil {
			return nil, fmt.Errorf("error scaning metrics: %w", err)
		}
		out = append(out, m)
	}

	return out, rows.Err()
}

const sandboxMetricsTimeRangeSelectQuery = `
    SELECT
        min(TimeUnix) AS start_time,
        max(TimeUnix) AS end_time
    FROM metrics_gauge m
    WHERE Attributes['sandbox_id'] = {sandbox_id:String}
      AND Attributes['team_id'] = {team_id:String}
`

const sandboxMetricsSelectQuery = `
SELECT
    toStartOfInterval(TimeUnix, INTERVAL {step:UInt32} SECOND) AS timestamp,

    maxIf(Value, MetricName = 'e2b.sandbox.cpu.total') AS cpu_total,
    maxIf(Value, MetricName = 'e2b.sandbox.cpu.used')  AS cpu_used,
    maxIf(Value, MetricName = 'e2b.sandbox.ram.total') AS ram_total,
    maxIf(Value, MetricName = 'e2b.sandbox.ram.used')  AS ram_used

FROM metrics_gauge

WHERE 
    Attributes['sandbox_id'] = {sandbox_id:String}
  AND Attributes['team_id'] = {team_id:String}
  AND MetricName IN (
        'e2b.sandbox.cpu.total',
        'e2b.sandbox.cpu.used',
        'e2b.sandbox.ram.total',
        'e2b.sandbox.ram.used'
    )
  AND TimeUnix >= {start_time:DateTime64}
  AND TimeUnix <= {end_time:DateTime64}
GROUP BY timestamp
ORDER BY timestamp;
`

func (c *Client) QuerySandboxTimeRange(ctx context.Context, sandboxID string, teamID string) (time.Time, time.Time, error) {
	var start, end time.Time

	err := c.conn.QueryRow(ctx, sandboxMetricsTimeRangeSelectQuery,
		clickhouse.Named("sandbox_id", sandboxID),
		clickhouse.Named("team_id", teamID),
	).Scan(&start, &end)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("query time range: %w", err)
	}

	return start, end, nil
}

func (c *Client) QuerySandboxMetrics(ctx context.Context, sandboxID string, teamID string, start time.Time, end time.Time, step time.Duration) ([]Metrics, error) {
	rows, err := c.conn.Query(ctx, sandboxMetricsSelectQuery,
		clickhouse.Named("sandbox_id", sandboxID),
		clickhouse.Named("team_id", teamID),
		clickhouse.DateNamed("start_time", start, clickhouse.Seconds),
		// Add an extra second to include the end time in the range
		clickhouse.DateNamed("end_time", end.Add(time.Second), clickhouse.Seconds),
		clickhouse.Named("step", strconv.Itoa(int(step.Seconds()))),
	)
	if err != nil {
		return nil, fmt.Errorf("query metrics5: %w", err)
	}

	defer rows.Close()
	var out []Metrics
	for rows.Next() {
		var m Metrics
		if err := rows.ScanStruct(&m); err != nil {
			return nil, fmt.Errorf("error scaning metrics: %w", err)
		}
		out = append(out, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over metrics rows: %w", err)
	}

	return out, nil
}
