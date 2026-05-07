// Package inspector implements the in-guest "did anything change?" signal
// consumed by the orchestrator's skipIfUnchanged checkpoint path.
// See issue #2580 and the design plan in
// /gpfs/users/sunpeng/.claude/plans/sparkling-gathering-crescent.md.
package inspector

import (
	"context"
	"os"
	"sync/atomic"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/inspector"
	spec "github.com/e2b-dev/infra/packages/envd/internal/services/spec/inspector/inspectorconnect"
)

// degradedReasonNoTracker is reported when the build did not include
// the `inspector_bpf` tag (envd was built without eBPF support). The
// orchestrator treats degraded responses as "changed" and falls
// through to a full checkpoint, preserving correctness.
const degradedReasonNoTracker = "fs tracker disabled (build without -tags inspector_bpf)"

// degradedReasonTrackerStartFailed is set when fsTracker.Start returns
// an error (kernel too old, BPF denied, etc.).
const degradedReasonTrackerStartFailed = "fs tracker failed to start"

// Config carries the runtime parameters needed by the trackers. Today
// only the proc tracker uses it; the fs tracker pulls cgroup ids from
// the AddTrackedCgroup RPC.
type Config struct {
	// CgroupPaths is the list of v2 cgroup directories envd uses for
	// user-spawned processes (typically /sys/fs/cgroup/{user,ptys,socats}).
	// Empty list disables process tracking.
	CgroupPaths []string
}

type Service struct {
	logger *zerolog.Logger
	epoch  atomic.Uint32

	fs               fsTracker
	proc             procTracker
	fsDegradedReason atomic.Value // string
}

func newService(l *zerolog.Logger, _ *execcontext.Defaults, cfg Config) *Service {
	s := &Service{
		logger: l,
		fs:     newFsTracker(),
		proc:   newProcTracker(cfg.CgroupPaths, os.Getpid()),
	}
	s.fsDegradedReason.Store("")
	return s
}

// Handle wires the InspectorService into the envd HTTP mux using the
// same Connect-RPC pattern as the process and filesystem services.
//
// Start() is invoked synchronously here so any kernel-level failures
// surface in envd startup logs (loud failure beats silent degradation).
func Handle(server *chi.Mux, l *zerolog.Logger, defaults *execcontext.Defaults, cfg Config) *Service {
	s := newService(l, defaults, cfg)

	if err := s.fs.Start(context.Background()); err != nil {
		s.fsDegradedReason.Store(degradedReasonTrackerStartFailed + ": " + err.Error())
		l.Warn().Err(err).Msg("inspector fs tracker disabled — falling through to full checkpoints")
	}

	// Establish an initial baseline so the first QueryChanges has
	// something to compare against. The result is discarded; we only
	// care about the side-effect of clearing soft-dirty bits.
	_, _ = s.proc.Reset()

	interceptors := connect.WithInterceptors(logs.NewUnaryLogInterceptor(l))
	path, h := spec.NewInspectorServiceHandler(s, interceptors)
	server.Mount(path, h)

	return s
}

// AddTrackedCgroup registers a v2 cgroup id with the underlying eBPF
// filter so any process spawned in that cgroup is observed. envd's
// process service should call this whenever it spawns into one of the
// per-process-type cgroups; without that wiring the fs tracker
// observes nothing and the inspector remains in degraded-by-emptiness
// mode (fs counter stays at zero).
func (s *Service) AddTrackedCgroup(cgroupID uint64) error {
	return s.fs.AddCgroup(cgroupID)
}

// RemoveTrackedCgroup is the symmetric removal call.
func (s *Service) RemoveTrackedCgroup(cgroupID uint64) error {
	return s.fs.RemoveCgroup(cgroupID)
}

// QueryChanges reports the inspector's current view. Both filesystem
// and process trackers contribute; either reporting "changed" sets the
// corresponding flag. A degraded response in either tracker propagates
// as the top-level degraded bit.
func (s *Service) QueryChanges(_ context.Context, _ *connect.Request[rpc.QueryChangesRequest]) (*connect.Response[rpc.QueryChangesResponse], error) {
	fsCount, fsOK := s.fs.Query()
	procChanged, procOK := s.proc.Query()

	resp := &rpc.QueryChangesResponse{
		FilesystemChanged: fsOK && fsCount > 0,
		ProcessesChanged:  procOK && procChanged,
		EpochId:           s.epoch.Load(),
		Degraded:          !fsOK || !procOK,
	}
	return connect.NewResponse(resp), nil
}

// ResetEpoch clears all per-epoch counters. The expected_epoch_id
// field guards against stale resets.
func (s *Service) ResetEpoch(_ context.Context, req *connect.Request[rpc.ResetEpochRequest]) (*connect.Response[rpc.ResetEpochResponse], error) {
	expected := req.Msg.GetExpectedEpochId()
	current := s.epoch.Load()

	if expected != 0 && expected != current {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errEpochMismatch{expected: expected, current: current})
	}

	_, _ = s.fs.Reset()
	_, _ = s.proc.Reset()
	next := s.epoch.Add(1)
	return connect.NewResponse(&rpc.ResetEpochResponse{NewEpochId: next}), nil
}

// Status reports the inspector's loaded capabilities for telemetry.
func (s *Service) Status(_ context.Context, _ *connect.Request[rpc.StatusRequest]) (*connect.Response[rpc.StatusResponse], error) {
	_, fsOK := s.fs.Query()
	reason, _ := s.fsDegradedReason.Load().(string)
	if !fsOK && reason == "" {
		reason = degradedReasonNoTracker
	}

	return connect.NewResponse(&rpc.StatusResponse{
		BpfLoaded:          fsOK,
		SoftDirtySupported: s.proc.SoftDirtySupported(),
		BtfPresent:         s.proc.BTFPresent(),
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
