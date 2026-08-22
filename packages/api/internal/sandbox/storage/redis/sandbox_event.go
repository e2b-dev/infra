package redis

import (
	"encoding/json"
	"strings"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
)

const (
	sandboxEventOpAdd    = "add"
	sandboxEventOpUpdate = "update"
	sandboxEventOpRemove = "remove"
)

// sandboxEvent is published on globalStorageNotifyChannel alongside existing
// plain-string routing keys. It carries the full sandbox payload so consumers
// can update their local cache without a follow-up Redis GET.
//
// Disambiguation: routing keys always begin with "sandbox:storage:" or "lock:",
// so a JSON object prefix "{" is an unambiguous discriminator.
type sandboxEvent struct {
	Op        string                `json:"op"`
	Sandbox   *sandboxtypes.Sandbox `json:"sandbox,omitempty"`
	SandboxID string                `json:"sandbox_id,omitempty"`
	TeamID    string                `json:"team_id,omitempty"`
}

// isSandboxEvent reports whether a pub/sub payload is a sandboxEvent rather
// than a plain routing-key string.
func isSandboxEvent(payload string) bool {
	return strings.HasPrefix(payload, "{")
}

// parseSandboxEvent deserializes a sandboxEvent from a pub/sub payload.
// Returns false if the payload is not a valid sandboxEvent.
func parseSandboxEvent(payload string) (sandboxEvent, bool) {
	var evt sandboxEvent
	if err := json.Unmarshal([]byte(payload), &evt); err != nil || evt.Op == "" {
		return sandboxEvent{}, false
	}

	return evt, true
}

// marshalSandboxEvent serializes a sandboxEvent to a JSON string for publishing.
func marshalSandboxEvent(evt sandboxEvent) (string, error) {
	data, err := json.Marshal(evt)
	if err != nil {
		return "", err
	}

	return string(data), nil
}
