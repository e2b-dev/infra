package event

import (
	"time"

	"github.com/google/uuid"
)

type SandboxEvent struct {
	Timestamp          time.Time `json:"timestamp"`
	SandboxID          string    `json:"sandbox_id"`
	SandboxExecutionID string    `json:"sandbox_execution_id"`
	SandboxTemplateID  string    `json:"sandbox_template_id"`
	SandboxBuildID     string    `json:"sandbox_build_id"`
	SandboxTeamID      uuid.UUID `json:"sandbox_team_id"`
	EventCategory      string    `json:"event_category"`
	EventLabel         string    `json:"event_label"`
	EventData          string    `json:"event_data,omitempty"`
}
