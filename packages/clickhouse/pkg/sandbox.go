package clickhouse

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Metrics struct {
	SandboxID      string    `ch:"sandbox_id"`
	TeamID         string    `ch:"team_id"`
	Timestamp      time.Time `ch:"ts"`
	CPUCount       float64   `ch:"cpu_total"`
	CPUUsedPercent float64   `ch:"cpu_used"`
	MemTotal       float64   `ch:"ram_total"`
	MemUsed        float64   `ch:"ram_used"`
	MemCache       float64   `ch:"ram_cache"`
	DiskTotal      float64   `ch:"disk_total"`
	DiskUsed       float64   `ch:"disk_used"`
}

// maxUnixSecondsForCH is the largest Unix-second value whose nanosecond
// representation fits in a signed int64. time.Time.UnixNano() is undefined
// beyond ~2262-04-11; API handlers accept Unix-second query params without
// an upper bound, so far-future sentinels (e.g. 9999999999 = year 2286)
// must be clamped before conversion to avoid wrapping to negative int64
// and silently breaking fromUnixTimestamp64Nano filter windows.
const maxUnixSecondsForCH = (1<<63 - 1) / int64(time.Second)

// unixNanoForCH returns t as nanoseconds since the Unix epoch, clamped to
// the int64-representable range. Always use this instead of t.UTC().UnixNano()
// when feeding values to ClickHouse's fromUnixTimestamp64Nano.
func unixNanoForCH(t time.Time) int64 {
	s := t.UTC().Unix()
	switch {
	case s > maxUnixSecondsForCH:
		s = maxUnixSecondsForCH
	case s < -maxUnixSecondsForCH:
		s = -maxUnixSecondsForCH
	}
	return time.Unix(s, 0).UTC().UnixNano()
}

var latestMetricsSelectQuery = fmt.Sprintf(`
SELECT sandbox_id,
       team_id,
       argMaxIf(value, timestamp, metric_name = '%s')  AS cpu_total,
       argMaxIf(value, timestamp, metric_name = '%s')  AS cpu_used,
       argMaxIf(value, timestamp, metric_name = '%s')  AS ram_total,
       argMaxIf(value, timestamp, metric_name = '%s')  AS ram_used,
       argMaxIf(value, timestamp, metric_name = '%s')  AS ram_cache,
       argMaxIf(value, timestamp, metric_name = '%s')  AS disk_total,
       argMaxIf(value, timestamp, metric_name = '%s')  AS disk_used,
       -- All metrics are recorded at the same time, so we can use max(timestamp) to get the latest one
       max(timestamp) as ts
FROM   sandbox_metrics_gauge
WHERE  sandbox_id IN ?
       AND team_id = ?
GROUP  BY sandbox_id,
          team_id; 
`, telemetry.SandboxCpuTotalGaugeName, telemetry.SandboxCpuUsedGaugeName, telemetry.SandboxRamTotalGaugeName, telemetry.SandboxRamUsedGaugeName, telemetry.SandboxRamCacheGaugeName, telemetry.SandboxDiskTotalGaugeName, telemetry.SandboxDiskUsedGaugeName)

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

var sandboxMetricsSelectQuery = fmt.Sprintf(`
SELECT   toStartOfInterval(timestamp, interval {step:UInt32} second) AS ts,
         maxIf(value, metric_name = '%s')         					 AS cpu_total,
         maxIf(value, metric_name = '%s')				          	 AS cpu_used,
         maxIf(value, metric_name = '%s')         					 AS ram_total,
         maxIf(value, metric_name = '%s')          					 AS ram_used,
         maxIf(value, metric_name = '%s')          					 AS ram_cache,
         maxIf(value, metric_name = '%s')        					 AS disk_total,
         maxIf(value, metric_name = '%s')         					 AS disk_used
FROM     sandbox_metrics_gauge s
WHERE    sandbox_id = {sandbox_id:String}
AND      team_id = {team_id:String}
AND      timestamp >= fromUnixTimestamp64Nano({start_time:Int64})
AND      timestamp <= fromUnixTimestamp64Nano({end_time:Int64})
GROUP BY ts
ORDER BY ts;
`, telemetry.SandboxCpuTotalGaugeName, telemetry.SandboxCpuUsedGaugeName, telemetry.SandboxRamTotalGaugeName, telemetry.SandboxRamUsedGaugeName, telemetry.SandboxRamCacheGaugeName, telemetry.SandboxDiskTotalGaugeName, telemetry.SandboxDiskUsedGaugeName)

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
		clickhouse.Named("start_time", unixNanoForCH(start)),
		clickhouse.Named("end_time", unixNanoForCH(end)),
		clickhouse.Named("step", strconv.Itoa(int(step.Seconds()))),
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

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over metrics rows: %w", err)
	}

	return out, nil
}
