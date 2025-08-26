package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ProductUsageCategory string

const (
	ProductUsageCategoryArugsAPI ProductUsageCategory = "argus_api"
)

type ProductUsage struct {
	Timestamp time.Time `ch:"timestamp"`
	TeamID    uuid.UUID `ch:"team_id"`
	Category  string    `ch:"category"`
	Action    string    `ch:"action"`
	Label     string    `ch:"label"`
}

const selectProductUsageByTeamIdQuery = `
SELECT
    timestamp,
    team_id,
    category,
    action,
    label
FROM product_usage
WHERE team_id = ?
ORDER BY timestamp %s
LIMIT ?
OFFSET ?
`

func (c *Client) SelectProductUsageByTeamId(ctx context.Context, teamID uuid.UUID, offset, limit int, orderAsc bool) ([]ProductUsage, error) {
	order := "DESC"
	if orderAsc {
		order = "ASC"
	}

	query := fmt.Sprintf(selectProductUsageByTeamIdQuery, order)
	rows, err := c.conn.Query(ctx, query, teamID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("error querying product usage by team id: %w", err)
	}
	defer rows.Close()

	var out []ProductUsage
	for rows.Next() {
		var m ProductUsage
		if err := rows.ScanStruct(&m); err != nil {
			return nil, fmt.Errorf("error scanning ProductUsage: %w", err)
		}
		out = append(out, m)
	}

	return out, rows.Err()
}
