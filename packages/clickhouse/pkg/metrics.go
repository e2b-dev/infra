package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

type Metrics struct {
	Timestamp      time.Time `ch:"timestamp"`
	SandboxID      string    `ch:"sandbox_id"`
	TeamID         string    `ch:"team_id"`
	CPUCount       uint32    `ch:"cpu_count"`
	CPUUsedPercent float32   `ch:"cpu_used_pct"`
	MemTotalMiB    uint64    `ch:"mem_total_mib"`
	MemUsedMiB     uint64    `ch:"mem_used_mib"`
}

const metricsInsertQuery = `
	INSERT INTO metrics (timestamp, sandbox_id, team_id, cpu_count, cpu_used_pct, mem_total_mib, mem_used_mib)
`

const metricsSelectQuery = `
    SELECT *
    FROM metrics
    WHERE sandbox_id = ?
      AND team_id    = ?
      AND timestamp  >= ?
    ORDER BY timestamp ASC
    LIMIT ?`

// InsertMetrics queues the record for the background goroutine.
func (c *Client) InsertMetrics(_ context.Context, m Metrics) error {
	select {
	case c.metricsCh <- m:
		return nil
	default:
		return errors.New("metrics channel is full")
	}
}

// QueryMetrics returns rows ordered by timestamp, paged by limit.
func (c *Client) QueryMetrics(ctx context.Context, sandboxID, teamID string, start time.Time, limit int) ([]Metrics, error) {
	rows, err := c.conn.Query(ctx, metricsSelectQuery, sandboxID, teamID, start, limit)
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

func (c *Client) metricsBatchLoop() {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	var batch []Metrics
	for {
		select {
		case m, ok := <-c.metricsCh:
			// channel closed; flush and exit
			if !ok {
				if err := c.flush(context.Background(), batch); err != nil {
					zap.L().Error("final metrics flush failed", zap.Int("count", len(batch)), zap.Error(err))
				}
				close(c.metricsChClosed)

				return
			}

			batch = append(batch, m)
			if len(batch) >= batchSize {
				if err := c.flush(context.Background(), batch); err != nil {
					zap.L().Error("batch size flush failed", zap.Int("count", len(batch)), zap.Error(err))
				}
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				if err := c.flush(context.Background(), batch); err != nil {
					zap.L().Error("periodic flush failed", zap.Int("count", len(batch)), zap.Error(err))
				}
				batch = batch[:0]
			}
		}
	}
}

// flush writes a slice of Metrics in one ClickHouse batch.
func (c *Client) flush(ctx context.Context, batch []Metrics) error {
	if len(batch) == 0 {
		return nil
	}

	// Flush shouldn't take longer than flushInterval
	ctx, cancel := context.WithTimeout(ctx, flushInterval)
	defer cancel()

	b, err := c.conn.PrepareBatch(ctx, metricsInsertQuery)
	if err != nil {
		return fmt.Errorf("prepare batch: %w", err)
	}

	for _, m := range batch {
		if err := b.AppendStruct(&m); err != nil {
			return fmt.Errorf("append metric: %w", err)
		}
	}

	if err := b.Send(); err != nil {
		return fmt.Errorf("send batch: %w", err)
	}

	zap.L().Debug("metrics batch flushed",
		zap.Int("records", len(batch)),
		zap.Time("oldest", batch[0].Timestamp),
		zap.Time("newest", batch[len(batch)-1].Timestamp),
	)

	return nil
}
