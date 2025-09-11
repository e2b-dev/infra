package events

import (
	"context"
	"time"
)

// NoopSandboxEventStore is a simple Noop that doesn't do anything
type NoopSandboxEventStore struct{}

func NewNoopSandboxEventStore() SandboxEventStore {
	return &NoopSandboxEventStore{}
}

func (m *NoopSandboxEventStore) SetSandboxIP(ctx context.Context, sandboxID string, sandboxIP string) error {
	// No-op
	return nil
}

func (m *NoopSandboxEventStore) GetSandboxID(ctx context.Context, sandboxIP string) (string, error) {
	// No-op
	return "", nil
}

func (m *NoopSandboxEventStore) DelSandboxIP(ctx context.Context, sandboxIP string) error {
	// No-op
	return nil
}

func (m *NoopSandboxEventStore) GetLastEvent(ctx context.Context, sandboxID string) (*SandboxEvent, error) {
	// No-op
	return nil, nil
}

func (m *NoopSandboxEventStore) GetLastNEvents(ctx context.Context, sandboxID string, n int) ([]*SandboxEvent, error) {
	// No-op
	return nil, nil
}

func (m *NoopSandboxEventStore) AddEvent(ctx context.Context, sandboxID string, event *SandboxEvent, expiration time.Duration) error {
	// No-op
	return nil
}

func (m *NoopSandboxEventStore) DelEvent(ctx context.Context, sandboxID string) error {
	// No-op
	return nil
}

func (m *NoopSandboxEventStore) Close(ctx context.Context) error {
	// No-op
	return nil
}
