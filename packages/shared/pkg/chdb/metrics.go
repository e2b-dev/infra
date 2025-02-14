package chdb

import (
	"context"
	"fmt"
	"log"

	"github.com/e2b-dev/infra/packages/shared/pkg/models/chmodels"
)

func (c *ClickHouseStore) InsertMetrics(ctx context.Context, metrics chmodels.Metrics) error {
	batch, err := c.Conn.PrepareBatch(ctx, "INSERT INTO metrics")
	if err != nil {
		return err
	}
	log.Printf("~~~insert metrics: %+v", metrics)
	err = batch.AppendStruct(&metrics)
	if err != nil {
		batch.Abort()
		return fmt.Errorf("failed to append metrics struct to clickhouse batcher: %w", err)
	}

	return batch.Send()
}

func (c *ClickHouseStore) QueryMetrics(ctx context.Context, sandboxID, teamID string, start int64, limit int) ([]chmodels.Metrics, error) {
	query := fmt.Sprintf(
		"SELECT * FROM metrics WHERE sandbox_id = '%s' AND team_id = '%s' AND timestamp >= %d LIMIT %d",
		sandboxID, teamID, start, limit,
	)

	rows, err := c.Query(ctx, query)
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
