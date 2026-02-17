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

	// If SandboxService/Create, extract isResume and store in the holder
	setIsResumeFromRS(ctx, rs)

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

// setIsResumeFromRS sets the isResume value in the context for SandboxService/Create
func setIsResumeFromRS(ctx context.Context, rs any) {
	holder, _ := ctx.Value(isResumeHolderKey{}).(*IsResumeHolder)
	if holder == nil {
		return
	}

	in, ok := rs.(*stats.InPayload)
	if !ok || in == nil {
		return
	}

	req, ok := in.Payload.(*orchestrator.SandboxCreateRequest)
	if !ok || req == nil {
		return
	}

	holder.Value = req.GetSandbox().GetSnapshot()
}
