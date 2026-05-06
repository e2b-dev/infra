// Package inspector implements the in-guest "did anything change?" signal
// consumed by the orchestrator's skipIfUnchanged checkpoint path.
// See issue #2580 and the design plan in
// /gpfs/users/sunpeng/.claude/plans/sparkling-gathering-crescent.md.
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

// degradedReasonNoTracker is reported when the build did not include the
// `inspector_bpf` tag (the Go binary was built without eBPF support).
// Callers must treat the response as "changed" and fall through to a
// full checkpoint, preserving correctness.
const degradedReasonNoTracker = "fs tracker disabled (build without -tags inspector_bpf)"

// degradedReasonTrackerStartFailed is set when Start() returns an error
// (kernel too old, BPF load denied, etc.). The tracker stays unloaded
// and Query/Reset return ok=false.
const degradedReasonTrackerStartFailed = "fs tracker failed to start"

type Service struct {
	logger *zerolog.Logger
	epoch  atomic.Uint32

	fs              fsTracker
	fsStartErr      atomic.Pointer[error]
	fsDegradedReason atomic.Value // string
}

func newService(l *zerolog.Logger, _ *execcontext.Defaults) *Service {
	s := &Service{
		logger: l,
		fs:     newFsTracker(),
	}
	s.fsDegradedReason.Store("")
	return s
}

// Handle wires the InspectorService into the envd HTTP mux using the
// same Connect-RPC pattern as the process and filesystem services.
// See packages/envd/internal/services/process/service.go:35.
//
// Start() is called synchronously here so that any kernel-level
// failures surface in envd startup logs (loud failure beats silent
// degradation).
func Handle(server *chi.Mux, l *zerolog.Logger, defaults *execcontext.Defaults) *Service {
	s := newService(l, defaults)

	if err := s.fs.Start(context.Background()); err != nil {
		err := err
		s.fsStartErr.Store(&err)
		s.fsDegradedReason.Store(degradedReasonTrackerStartFailed + ": " + err.Error())
		l.Warn().Err(err).Msg("inspector fs tracker disabled — falling through to full checkpoints")
	}

	interceptors := connect.WithInterceptors(logs.NewUnaryLogInterceptor(l))
	path, h := spec.NewInspectorServiceHandler(s, interceptors)
	server.Mount(path, h)

	return s
}

// AddTrackedCgroup registers a v2 cgroup id with the underlying eBPF
// filter so any process spawned in that cgroup is observed. envd's
// process service should call this whenever it creates a per-process
// cgroup (see PR 3 follow-up for the wiring point).
func (s *Service) AddTrackedCgroup(cgroupID uint64) error {
	return s.fs.AddCgroup(cgroupID)
}

// RemoveTrackedCgroup is the symmetric removal call.
func (s *Service) RemoveTrackedCgroup(cgroupID uint64) error {
	return s.fs.RemoveCgroup(cgroupID)
}

// QueryChanges reports the inspector's current view. The boolean
// degraded conveys whether the tracker is healthy — when it is not,
// callers MUST treat the response as "changed".
func (s *Service) QueryChanges(_ context.Context, _ *connect.Request[rpc.QueryChangesRequest]) (*connect.Response[rpc.QueryChangesResponse], error) {
	count, ok := s.fs.Query()
	degraded := !ok

	resp := &rpc.QueryChangesResponse{
		FilesystemChanged: ok && count > 0,
		ProcessesChanged:  false, // wired in PR 3
		EpochId:           s.epoch.Load(),
		Degraded:          degraded,
	}
	return connect.NewResponse(resp), nil
}

// ResetEpoch clears all per-epoch counters. The expected_epoch_id field
// guards against stale resets (a caller that holds an old epoch must
// not silently overwrite a newer one).
func (s *Service) ResetEpoch(_ context.Context, req *connect.Request[rpc.ResetEpochRequest]) (*connect.Response[rpc.ResetEpochResponse], error) {
	expected := req.Msg.GetExpectedEpochId()
	current := s.epoch.Load()

	if expected != 0 && expected != current {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errEpochMismatch{expected: expected, current: current})
	}

	_, _ = s.fs.Reset() // best-effort; ok=false leaves us in degraded mode
	next := s.epoch.Add(1)
	return connect.NewResponse(&rpc.ResetEpochResponse{NewEpochId: next}), nil
}

// Status reports the inspector's loaded capabilities for telemetry.
func (s *Service) Status(_ context.Context, _ *connect.Request[rpc.StatusRequest]) (*connect.Response[rpc.StatusResponse], error) {
	_, ok := s.fs.Query()
	reason, _ := s.fsDegradedReason.Load().(string)
	if !ok && reason == "" {
		reason = degradedReasonNoTracker
	}

	return connect.NewResponse(&rpc.StatusResponse{
		BpfLoaded:          ok,
		SoftDirtySupported: false, // PR 3
		BtfPresent:         false, // PR 3
		DegradedReason:     reason,
	}), nil
}

type errEpochMismatch struct {
	expected uint32
	current  uint32
}

func (e errEpochMismatch) Error() string {
	return "inspector epoch mismatch"
}
