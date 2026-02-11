package grpc

import (
	"context"

	"google.golang.org/grpc/stats"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type noTraceKey struct{}

var noTrace = struct{}{}

// isResumeHolderKey is a context key for storing the isResume holder.
type isResumeHolderKey struct{}

// IsResumeHolder holds a mutable isResume value that can be set in HandleRPC
// (when InPayload is received) and read by extractIsResume during metric recording.
type IsResumeHolder struct {
	Value bool
}

// statsWrapper wraps grpc stats.Handler and removes healthchecks from tracing.
// It also extracts the isResume attribute from SandboxCreateRequest for metrics.
type statsWrapper struct {
	statsHandler stats.Handler
}

// NewStatsWrapper wraps grpc stats.Handler and removes healthchecks from tracing.
func NewStatsWrapper(statsHandler stats.Handler) stats.Handler {
	return &statsWrapper{statsHandler: statsHandler}
}

// HandleConn exists to satisfy gRPC stats.Handler.
func (s *statsWrapper) HandleConn(context.Context, stats.ConnStats) {
	// no-op
}

// TagConn exists to satisfy gRPC stats.Handler.
func (s *statsWrapper) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	// no-op
	return ctx
}

// HandleRPC implements per-RPC tracing and stats instrumentation.
func (s *statsWrapper) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	// Check if the context contains noTraceKey, and trace only when its
	// not present.
	_, skip := ctx.Value(noTraceKey{}).(struct{})
	if skip {
		return
	}

	// On InPayload for SandboxService/Create, extract isResume and store in the holder
	// so that extractIsResume can read it when metrics are recorded.
	if holder, ok := ctx.Value(isResumeHolderKey{}).(*IsResumeHolder); ok {
		if payload, ok := rs.(*stats.InPayload); ok {
			if req, ok := payload.Payload.(*orchestrator.SandboxCreateRequest); ok {
				if sandbox := req.GetSandbox(); sandbox != nil {
					isResume := sandbox.GetSnapshot()
					holder.Value = isResume
				}
			}
		}
	}

	s.statsHandler.HandleRPC(ctx, rs)
}

// TagRPC implements per-RPC context management.
func (s *statsWrapper) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	if rti.FullMethodName == "/grpc.health.v1.Health/Check" {
		// Add to context we don't want to trace this.
		return context.WithValue(ctx, noTraceKey{}, noTrace)
	}

	// For SandboxService/Create, store a holder that we can mutate in HandleRPC
	// when the request payload is received.
	if rti.FullMethodName == "/SandboxService/Create" {
		ctx = context.WithValue(ctx, isResumeHolderKey{}, &IsResumeHolder{})
	}

	return s.statsHandler.TagRPC(ctx, rti)
}
