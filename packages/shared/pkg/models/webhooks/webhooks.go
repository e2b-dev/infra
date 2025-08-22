package webhooks

import (
	"slices"
	"time"

	"github.com/google/uuid"
)

type SandboxLifecycleEvent string

const (
	SandboxLifecycleEventCreate SandboxLifecycleEvent = "create"
	SandboxLifecycleEventKill   SandboxLifecycleEvent = "kill"
	SandboxLifecycleEventPause  SandboxLifecycleEvent = "pause"
	SandboxLifecycleEventResume SandboxLifecycleEvent = "resume"
	SandboxLifecycleEventUpdate SandboxLifecycleEvent = "update"
)

var AllowedLifecycleEvents = []string{
	string(SandboxLifecycleEventCreate),
	string(SandboxLifecycleEventKill),
	string(SandboxLifecycleEventPause),
	string(SandboxLifecycleEventResume),
	string(SandboxLifecycleEventUpdate),
}

func IsLifecycleEvent(event string) bool {
	return slices.Contains(AllowedLifecycleEvents, event)
}

type SandboxWebhooksMetaData struct {
	WebhookID uuid.UUID               `json:"webhook_id"`
	Events    []SandboxLifecycleEvent `json:"events"`
	URL       string                  `json:"url"`
}

type SandboxWebhooksPayload struct {
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
