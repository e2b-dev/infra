package chdb

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/chdb/chmodels"
)

func (c *ClickHouseStore) InsertMetrics(ctx context.Context, metrics chmodels.Metrics) error {
	batch, err := c.Conn.PrepareBatch(ctx, "INSERT INTO metrics")
	if err != nil {
		return err
	}
	err = batch.AppendStruct(&metrics)
	if err != nil {
		batch.Abort()
		return fmt.Errorf("failed to append metrics struct to clickhouse batcher: %w", err)
	}

	return batch.Send()
}

func (c *ClickHouseStore) QueryMetrics(ctx context.Context, sandboxID, teamID string, start int64, limit int) ([]chmodels.Metrics, error) {
	query := "SELECT * FROM metrics WHERE sandbox_id = (?) AND team_id = (?) AND timestamp >= (?) LIMIT (?)"

	rows, err := c.Query(ctx, query, sandboxID, teamID, start, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []chmodels.Metrics
	for rows.Next() {
		var metric chmodels.Metrics
		if err := rows.ScanStruct(&metric); err != nil {
			return nil, err
		}
		metrics = append(metrics, metric)
	}

	return metrics, rows.Err()
}
