//go:build !linux
// +build !linux

package build

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"
)

type FCNetwork struct {
	namespaceID string
}

// Cleanup is a no-op for non-Linux systems
func (n *FCNetwork) Cleanup(ctx context.Context, tracer trace.Tracer) {
}

// NewFCNetwork returns an error
func NewFCNetwork(ctx context.Context, tracer trace.Tracer, env *Env) (*FCNetwork, error) {
	return nil, fmt.Errorf("network functionality is only supported on Linux")
}
