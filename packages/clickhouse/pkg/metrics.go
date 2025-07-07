package clickhouse

import (
	"context"
	"fmt"
	"time"
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

const metricsSelectQuery = `
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

	rows, err := c.conn.Query(ctx, metricsSelectQuery,
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
