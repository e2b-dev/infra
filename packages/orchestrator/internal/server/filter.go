package server

import (
	"context"

	"google.golang.org/grpc/stats"
)

type noTraceKey struct{}

var noTrace = struct{}{}

// statsWrapper wraps grpc stats.Handler and removes healthchecks from tracing.
type statsWrapper struct {
	statsHandler stats.Handler
}

// NewStatsWrapper wraps grpc stats.Handler and removes healthchecks from tracing.
func NewStatsWrapper(statsHandler stats.Handler) stats.Handler {
	return &statsWrapper{statsHandler: statsHandler}
}

// HandleConn exists to satisfy gRPC stats.Handler.
func (s *statsWrapper) HandleConn(ctx context.Context, cs stats.ConnStats) {
	// no-op
}

// TagConn exists to satisfy gRPC stats.Handler.
func (s *statsWrapper) TagConn(ctx context.Context, cti *stats.ConnTagInfo) context.Context {
	// no-op
	return ctx
}

// HandleRPC implements per-RPC tracing and stats instrumentation.
func (s *statsWrapper) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	// Check if the context contains noTraceKey, and trace only when its
	// not present.
	_, ok := ctx.Value(noTraceKey{}).(struct{})
	if !ok {
		s.statsHandler.HandleRPC(ctx, rs)
	}
}

// TagRPC implements per-RPC context management.
func (s *statsWrapper) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	if rti.FullMethodName == "/grpc.health.v1.Health/Check" {
		// Add to context we don't want to trace this.
		return context.WithValue(ctx, noTraceKey{}, noTrace)
	}
	return s.statsHandler.TagRPC(ctx, rti)
}
