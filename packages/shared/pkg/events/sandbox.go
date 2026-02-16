package events

import (
	"time"

	"github.com/google/uuid"
)

const (
	StructureVersionV1 = "v1"
	StructureVersionV2 = "v2"
)

const (
	SandboxCreatedEvent      = "sandbox.lifecycle.created"
	SandboxKilledEvent       = "sandbox.lifecycle.killed"
	SandboxPausedEvent       = "sandbox.lifecycle.paused"
	SandboxResumedEvent      = "sandbox.lifecycle.resumed"
	SandboxUpdatedEvent      = "sandbox.lifecycle.updated"
	SandboxCheckpointedEvent = "sandbox.lifecycle.checkpointed"
)

var ValidSandboxEventTypes = []string{
	SandboxCreatedEvent,
	SandboxKilledEvent,
	SandboxPausedEvent,
	SandboxResumedEvent,
	SandboxUpdatedEvent,
	SandboxCheckpointedEvent,
}

type SandboxEvent struct {
	ID        uuid.UUID `json:"id"`
	Version   string    `json:"version"`
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`

	// Deprecated: for new events use event field with dot syntax
	EventCategory string `json:"event_category"`
	// Deprecated: for new events use event field with dot syntax
	EventLabel string         `json:"event_label"`
	EventData  map[string]any `json:"event_data,omitempty"`

	SandboxID          string    `json:"sandbox_id"`
	SandboxExecutionID string    `json:"sandbox_execution_id"`
	SandboxTemplateID  string    `json:"sandbox_template_id"`
	SandboxBuildID     string    `json:"sandbox_build_id"`
	SandboxTeamID      uuid.UUID `json:"sandbox_team_id"`
}
