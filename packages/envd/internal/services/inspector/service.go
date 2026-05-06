// Package inspector implements the in-guest "did anything change?" signal
// consumed by the orchestrator's skipIfUnchanged checkpoint path.
// See issue #2580 and the design plan in
// /gpfs/users/sunpeng/.claude/plans/sparkling-gathering-crescent.md.
//
// This file is the scaffold landed in PR 1: QueryChanges always returns
// degraded=true, so any caller falls through to a full checkpoint. The
// real trackers land in PR 2 (filesystem) and PR 3 (process).
package inspector

import (
	"context"
	"sync/atomic"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/inspector"
	spec "github.com/e2b-dev/infra/packages/envd/internal/services/spec/inspector/inspectorconnect"
)

// degradedReasonScaffold is reported by the scaffold implementation until
// the trackers ship. It's a sentinel string so log greppers can spot
// callers using a host that hasn't picked up the eBPF tracker yet.
const degradedReasonScaffold = "scaffold: trackers not yet implemented"

type Service struct {
	logger *zerolog.Logger
	epoch  atomic.Uint32

	// Tracker fields populated in PR 2 / PR 3. Left nil here on purpose
	// so the scaffold path is dead-obvious in code review.
}

func newService(l *zerolog.Logger, _ *execcontext.Defaults) *Service {
	return &Service{logger: l}
}

// Handle wires the InspectorService into the envd HTTP mux using the
// same Connect-RPC pattern as the process and filesystem services.
// See packages/envd/internal/services/process/service.go:35.
func Handle(server *chi.Mux, l *zerolog.Logger, defaults *execcontext.Defaults) *Service {
	s := newService(l, defaults)

	interceptors := connect.WithInterceptors(logs.NewUnaryLogInterceptor(l))
	path, h := spec.NewInspectorServiceHandler(s, interceptors)
	server.Mount(path, h)

	return s
}

// QueryChanges always reports degraded=true on the scaffold so callers
// fall through to a full checkpoint. No correctness risk.
func (s *Service) QueryChanges(_ context.Context, _ *connect.Request[rpc.QueryChangesRequest]) (*connect.Response[rpc.QueryChangesResponse], error) {
	return connect.NewResponse(&rpc.QueryChangesResponse{
		FilesystemChanged: false,
		ProcessesChanged:  false,
		EpochId:           s.epoch.Load(),
		Degraded:          true,
	}), nil
}

// ResetEpoch increments the epoch counter. The scaffold implementation
// has no per-epoch state to clear; the trackers in PR 2 / PR 3 will
// hook in here.
func (s *Service) ResetEpoch(_ context.Context, req *connect.Request[rpc.ResetEpochRequest]) (*connect.Response[rpc.ResetEpochResponse], error) {
	expected := req.Msg.GetExpectedEpochId()
	current := s.epoch.Load()

	if expected != 0 && expected != current {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errEpochMismatch{expected: expected, current: current})
	}

	next := s.epoch.Add(1)
	return connect.NewResponse(&rpc.ResetEpochResponse{NewEpochId: next}), nil
}

// Status reports the scaffold capabilities. All probes will be wired in
// PR 3 alongside the soft-dirty / BTF detection.
func (s *Service) Status(_ context.Context, _ *connect.Request[rpc.StatusRequest]) (*connect.Response[rpc.StatusResponse], error) {
	return connect.NewResponse(&rpc.StatusResponse{
		BpfLoaded:           false,
		SoftDirtySupported:  false,
		BtfPresent:          false,
		DegradedReason:      degradedReasonScaffold,
	}), nil
}

type errEpochMismatch struct {
	expected uint32
	current  uint32
}

func (e errEpochMismatch) Error() string {
	return "inspector epoch mismatch"
}
