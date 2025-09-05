package webhooks

import (
	"fmt"
	"slices"

	"github.com/google/uuid"
)

const WebhookKeyPrefix = "wh"

const WebhooksChannel = "sandbox-webhooks"

func DeriveKey(teamID uuid.UUID) string {
	return fmt.Sprintf("%s:%s", WebhookKeyPrefix, teamID.String())
}

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
	Events []SandboxLifecycleEvent `json:"events"`
	URL    string                  `json:"url"`
}
