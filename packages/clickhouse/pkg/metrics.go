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
	DiskTotal      float64   `ch:"disk_total"`
	DiskUsed       float64   `ch:"disk_used"`
}

const latestMetricsSelectQuery = `
SELECT sandbox_id,
       team_id,
       argMaxIf(value, timestamp, metric_name = 'e2b.sandbox.cpu.total')  AS cpu_total,
       argMaxIf(value, timestamp, metric_name = 'e2b.sandbox.cpu.used')   AS cpu_used,
       argMaxIf(value, timestamp, metric_name = 'e2b.sandbox.ram.total')  AS ram_total,
       argMaxIf(value, timestamp, metric_name = 'e2b.sandbox.ram.used')   AS ram_used,
       argMaxIf(value, timestamp, metric_name = 'e2b.sandbox.disk.total') AS disk_total,
       argMaxIf(value, timestamp, metric_name = 'e2b.sandbox.disk.used')  AS disk_used
FROM   sandbox_metrics_gauge
WHERE  sandbox_id IN ?
       AND team_id = ?
GROUP  BY sandbox_id,
          team_id; 
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
SELECT Min(timestamp) AS start_time,
       Max(timestamp) AS end_time
FROM   sandbox_metrics_gauge m
WHERE  sandbox_id = {sandbox_id:String}
       AND team_id = {team_id:String};
`

const sandboxMetricsSelectQuery = `
SELECT   toStartOfInterval(s.timestamp, interval {step:UInt32} second) AS timestamp,
         maxIf(value, metric_name = 'e2b.sandbox.cpu.total')          AS cpu_total,
         maxIf(value, metric_name = 'e2b.sandbox.cpu.used')           AS cpu_used,
         maxIf(value, metric_name = 'e2b.sandbox.ram.total')          AS ram_total,
         maxIf(value, metric_name = 'e2b.sandbox.ram.used')           AS ram_used,
         maxIf(value, metric_name = 'e2b.sandbox.disk.total')         AS disk_total,
         maxIf(value, metric_name = 'e2b.sandbox.disk.used')          AS disk_used
FROM     sandbox_metrics_gauge s
WHERE    sandbox_id = {sandbox_id:String}
AND      team_id = {team_id:String}
AND      s.timestamp >= {start_time:DateTime64}
AND      s.timestamp <= {end_time:DateTime64}
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
