package events

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type SandboxEvent struct {
	ID        uuid.UUID `ch:"id"`
	Version   string    `ch:"version"`
	Type      string    `ch:"type"`
	Timestamp time.Time `ch:"timestamp"`

	EventData          sql.NullString `ch:"event_data"`
	SandboxID          string         `ch:"sandbox_id"`
	SandboxExecutionID string         `ch:"sandbox_execution_id"`
	SandboxTemplateID  string         `ch:"sandbox_template_id"`
	SandboxBuildID     string         `ch:"sandbox_build_id"`
	SandboxTeamID      uuid.UUID      `ch:"sandbox_team_id"`
}
