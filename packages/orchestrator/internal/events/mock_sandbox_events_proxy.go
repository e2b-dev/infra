package events

import (
	"context"
)

// NoopSandboxEventProxy is a simple Noop that doesn't do anything
type NoopSandboxEventProxy struct{}

func NewNoopSandboxEventProxy() *NoopSandboxEventProxy {
	return &NoopSandboxEventProxy{}
}

func (m *NoopSandboxEventProxy) Start() error {
	// No-op
	return nil
}

func (m *NoopSandboxEventProxy) Close(ctx context.Context) error {
	// No-op
	return nil
}
