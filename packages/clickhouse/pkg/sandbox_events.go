package clickhouse

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type SandboxEventCategory string

const (
	SandboxEventCategoryLifecycle SandboxEventCategory = "lifecycle"
	SandboxEventCategoryMetric    SandboxEventCategory = "metric"
	SandboxEventCategoryProcess   SandboxEventCategory = "process"
	SandboxEventCategoryNetwork   SandboxEventCategory = "network"
	SandboxEventCategoryFile      SandboxEventCategory = "file"
	SandboxEventCategoryError     SandboxEventCategory = "error"
)

type SandboxEventLabel string

const (
	SandboxEventLabelCreate SandboxEventLabel = "create"
	SandboxEventLabelPause  SandboxEventLabel = "pause"
	SandboxEventLabelResume SandboxEventLabel = "resume"
	SandboxEventLabelUpdate SandboxEventLabel = "update"
	SandboxEventLabelKill   SandboxEventLabel = "kill"
)

type SandboxEvent struct {
	Timestamp          time.Time      `ch:"timestamp"`
	SandboxID          string         `ch:"sandbox_id"`
	SandboxExecutionID string         `ch:"sandbox_execution_id"`
	SandboxTemplateID  string         `ch:"sandbox_template_id"`
	SandboxBuildID     string         `ch:"sandbox_build_id"`
	SandboxTeamID      uuid.UUID      `ch:"sandbox_team_id"`
	EventCategory      string         `ch:"event_category"`
	EventLabel         string         `ch:"event_label"`
	EventData          sql.NullString `ch:"event_data"`
}

const existsSandboxIdQuery = `
SELECT 1 FROM sandbox_events WHERE sandbox_id = ? LIMIT 1
`

func (c *Client) ExistsSandboxId(ctx context.Context, sandboxID string) (bool, error) {
	rows, err := c.conn.Query(ctx, existsSandboxIdQuery, sandboxID)
	if err != nil {
		return false, fmt.Errorf("error querying sandbox exists by sandbox id: %w", err)
	}
	defer rows.Close()

	return rows.Next(), rows.Err()
}

const selectSandboxEventsBySandboxIdQuery = `
SELECT
    timestamp,
    sandbox_id,
    sandbox_execution_id,
    sandbox_template_id,
    sandbox_build_id,
    sandbox_team_id,
    event_category,
    event_label,
    event_data
FROM sandbox_events
WHERE sandbox_id = ?
ORDER BY timestamp %s
LIMIT ?
OFFSET ?
`

func (c *Client) SelectSandboxEventsBySandboxId(ctx context.Context, sandboxID string, offset, limit int, orderAsc bool) ([]SandboxEvent, error) {
	order := "DESC"
	if orderAsc {
		order = "ASC"
	}

	query := fmt.Sprintf(selectSandboxEventsBySandboxIdQuery, order)
	rows, err := c.conn.Query(ctx, query, sandboxID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("error querying sandbox events by sandbox id: %w", err)
	}
	defer rows.Close()

	var out []SandboxEvent
	for rows.Next() {
		var m SandboxEvent
		if err := rows.ScanStruct(&m); err != nil {
			return nil, fmt.Errorf("error scanning SandboxEvent: %w", err)
		}
		out = append(out, m)
	}

	return out, rows.Err()
}

const selectSandboxEventsByTeamIdQuery = `
SELECT
    timestamp,
    sandbox_id,
    sandbox_execution_id,
    sandbox_template_id,
    sandbox_build_id,
    sandbox_team_id,
    event_category,
    event_label,
    event_data
FROM sandbox_events
WHERE sandbox_team_id = ?
ORDER BY timestamp %s
LIMIT ?
OFFSET ?
`

func (c *Client) SelectSandboxEventsByTeamId(ctx context.Context, teamID uuid.UUID, offset, limit int, orderAsc bool) ([]SandboxEvent, error) {
	order := "DESC"
	if orderAsc {
		order = "ASC"
	}

	query := fmt.Sprintf(selectSandboxEventsByTeamIdQuery, order)

	rows, err := c.conn.Query(ctx, query, teamID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("error querying sandbox events by team id: %w", err)
	}
	defer rows.Close()

	var out []SandboxEvent
	for rows.Next() {
		var m SandboxEvent
		if err := rows.ScanStruct(&m); err != nil {
			return nil, fmt.Errorf("error scanning SandboxEvent: %w", err)
		}
		out = append(out, m)
	}

	return out, rows.Err()
}
