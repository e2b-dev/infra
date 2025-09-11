package events

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/events/event"
)

// NoopSandboxEventsService is a simple Noop that doesn't do anything
type NoopSandboxEventsService struct{}

func NewNoopSandboxEventsService() *NoopSandboxEventsService {
	return &NoopSandboxEventsService{}
}

func (m *NoopSandboxEventsService) HandleEvent(ctx context.Context, event event.SandboxEvent) {
	// No-op
}

func (m *NoopSandboxEventsService) Close(ctx context.Context) error {
	// No-op
	return nil
}
