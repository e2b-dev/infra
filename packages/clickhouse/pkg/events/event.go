package events

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type SandboxEvent struct {
	ID      uuid.UUID `ch:"id"`
	Version string    `ch:"version"`
	Type    string    `ch:"type"`

	EventCategory string         `ch:"event_category"`
	EventLabel    string         `ch:"event_label"`
	EventData     sql.NullString `ch:"event_data"`

	Timestamp          time.Time `ch:"timestamp"`
	SandboxID          string    `ch:"sandbox_id"`
	SandboxExecutionID string    `ch:"sandbox_execution_id"`
	SandboxTemplateID  string    `ch:"sandbox_template_id"`
	SandboxBuildID     string    `ch:"sandbox_build_id"`
	SandboxTeamID      uuid.UUID `ch:"sandbox_team_id"`
}
