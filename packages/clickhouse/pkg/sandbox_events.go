package clickhouse

import (
	"context"
	"fmt"
	"time"
)

type SandboxEvent struct {
	Timestamp          time.Time `ch:"timestamp"`
	SandboxID          string    `ch:"sandbox_id"`
	SandboxExecutionID string    `ch:"sandbox_execution_id"`
	SandboxTemplateID  string    `ch:"sandbox_template_id"`
	SandboxTeamID      string    `ch:"sandbox_team_id"`
	EventCategory      string    `ch:"event_category"`
	EventLabel         string    `ch:"event_label"`
	EventData          *string   `ch:"event_data"`
}

const latestSandboxEventSelectQuery = `
SELECT
    timestamp,
    sandbox_id,
    sandbox_execution_id,
    sandbox_template_id,
    sandbox_team_id,
    event_category,
    event_label,
FROM sandbox_events_local
WHERE sandbox_id = ?
ORDER BY timestamp DESC
LIMIT ?
OFFSET ?
`

func (c *Client) QuerySandboxEvents(ctx context.Context, sandboxID string, offset, limit int) ([]SandboxEvent, error) {
	rows, err := c.conn.Query(ctx, latestSandboxEventSelectQuery,
		sandboxID,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("error querying sandbox events: %w", err)
	}
	defer rows.Close()

	var out []SandboxEvent
	for rows.Next() {
		var m SandboxEvent
		if err := rows.ScanStruct(&m); err != nil {
			return nil, fmt.Errorf("error scaning SandboxEvent: %w", err)
		}
		out = append(out, m)
	}

	return out, rows.Err()
}

const insertSandboxEventQuery = `
INSERT INTO sandbox_events_local (
    timestamp,
    sandbox_id, 
    sandbox_execution_id,
    sandbox_template_id,
    sandbox_team_id,
    event_category,
    event_label,
    event_data
) VALUES (
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
)`

func (c *Client) InsertSandboxEvent(ctx context.Context, event SandboxEvent) error {
	return c.conn.Exec(ctx, insertSandboxEventQuery,
		time.Now().UTC(),
		event.SandboxID,
		event.SandboxExecutionID,
		event.SandboxTemplateID,
		event.SandboxTeamID,
		event.EventCategory,
		event.EventLabel,
		event.EventData,
	)
}
