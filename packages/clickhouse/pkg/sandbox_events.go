package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
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
	Timestamp          time.Time `ch:"timestamp"`
	SandboxID          string    `ch:"sandbox_id"`
	SandboxExecutionID string    `ch:"sandbox_execution_id"`
	SandboxTemplateID  string    `ch:"sandbox_template_id"`
	SandboxBuildID     string    `ch:"sandbox_build_id"`
	SandboxTeamID      string    `ch:"sandbox_team_id"`
	EventCategory      string    `ch:"event_category"`
	EventLabel         string    `ch:"event_label"`
	EventData          string    `ch:"event_data"`
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
WHERE sandbox_id = {sandbox_id:String}
ORDER BY timestamp %s
LIMIT {limit:UInt32}
OFFSET {offset:UInt32}
`

func (c *Client) SelectSandboxEventsBySandboxId(ctx context.Context, sandboxID string, offset, limit int, orderAsc bool) ([]SandboxEvent, error) {
	order := "DESC"
	if orderAsc {
		order = "ASC"
	}

	query := fmt.Sprintf(selectSandboxEventsBySandboxIdQuery, order)
	rows, err := c.conn.Query(ctx, query,
		clickhouse.Named("sandbox_id", sandboxID),
		clickhouse.Named("limit", limit),
		clickhouse.Named("offset", offset),
	)
	if err != nil {
		return nil, fmt.Errorf("error querying sandbox events by sandbox id: %w", err)
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
WHERE sandbox_team_id = {team_id:UUID}
ORDER BY timestamp %s
LIMIT {limit:UInt32}
OFFSET {offset:UInt32}
`

func (c *Client) SelectSandboxEventsByTeamId(ctx context.Context, teamID uuid.UUID, offset, limit int, orderAsc bool) ([]SandboxEvent, error) {
	order := "DESC"
	if !orderAsc {
		order = "ASC"
	}

	query := fmt.Sprintf(selectSandboxEventsByTeamIdQuery, order)

	rows, err := c.conn.Query(ctx, query,
		clickhouse.Named("team_id", teamID),
		clickhouse.Named("limit", limit),
		clickhouse.Named("offset", offset),
	)
	if err != nil {
		return nil, fmt.Errorf("error querying sandbox events by team id: %w", err)
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

// These SETTINGS allow inserts in async mode, which is batching (intermittent buffer flushing) managed by ClickHouse.
// More info:
// - https://clickhouse.com/docs/operations/settings/settings#async_insert
// - https://clickhouse.com/docs/operations/settings/settings#wait_for_async_insert
const insertSandboxEventQueryAsync = `
INSERT INTO sandbox_events
(
    timestamp,
    sandbox_id, 
    sandbox_execution_id,
    sandbox_template_id,
    sandbox_build_id,
    sandbox_team_id,
    event_category,
    event_label,
    event_data
)
SETTINGS async_insert=1, wait_for_async_insert=1
VALUES (
    {timestamp:DateTime64(9)},
    {sandbox_id:String},
    {sandbox_execution_id:String},
    {sandbox_template_id:String},
    {sandbox_build_id:String},
    {sandbox_team_id:UUID},
    {event_category:String},
    {event_label:String},
    {event_data:String}
)`

func (c *Client) InsertSandboxEvent(ctx context.Context, event SandboxEvent) error {
	return c.conn.Exec(ctx, insertSandboxEventQueryAsync,
		clickhouse.Named("timestamp", time.Now().UTC()),
		clickhouse.Named("sandbox_id", event.SandboxID),
		clickhouse.Named("sandbox_execution_id", event.SandboxExecutionID),
		clickhouse.Named("sandbox_template_id", event.SandboxTemplateID),
		clickhouse.Named("sandbox_build_id", event.SandboxBuildID),
		clickhouse.Named("sandbox_team_id", event.SandboxTeamID),
		clickhouse.Named("event_category", event.EventCategory),
		clickhouse.Named("event_label", event.EventLabel),
		clickhouse.Named("event_data", event.EventData),
	)
