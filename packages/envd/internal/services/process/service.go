package process

import (
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
	"github.com/e2b-dev/infra/packages/envd/internal/services/process/handler"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
	spec "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process/processconnect"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

// terminatedRetentionTTL is how long a process's terminal event is retained
// after it exits, so a late Connect can still recover the exit code. This
// covers the live-upgrade handover gap: if a process
// exits while no client is subscribed, its EndEvent is dropped by the
// multiplex, and a reconnecting client would otherwise never learn the exit
// code. The cache keeps only the terminal event (the exit code), which is
// enough for a reconnecting client to learn how the process ended; buffering
// the full missed output stream for replay is a possible later enhancement, not
// done here.
const terminatedRetentionTTL = 30 * time.Second

// retainedExit is a process's terminal event, kept in the retention cache for
// terminatedRetentionTTL after the process exits.
type retainedExit struct {
	pid    uint32
	tag    *string
	end    *rpc.ProcessEvent_EndEvent
	expiry time.Time // when this entry is evicted; carried across a live-upgrade
}

type Service struct {
	processes  *utils.Map[uint32, *handler.Handler]
	terminated *utils.Map[uint32, *retainedExit]
	logger     *zerolog.Logger
	defaults   *execcontext.Defaults
	// cgroupManager places spawned processes into their cgroup; workloadFreezer
	// freezes/thaws the workload during a live-upgrade handover. The freezer is
	// shared with the HTTP API so both serialize on one lock.
	cgroupManager   cgroups.Manager
	workloadFreezer *cgroups.WorkloadFreezer
}

func newService(l *zerolog.Logger, defaults *execcontext.Defaults, workloadFreezer *cgroups.WorkloadFreezer) *Service {
	return &Service{
		logger:          l,
		processes:       utils.NewMap[uint32, *handler.Handler](),
		terminated:      utils.NewMap[uint32, *retainedExit](),
		defaults:        defaults,
		cgroupManager:   workloadFreezer.Manager(),
		workloadFreezer: workloadFreezer,
	}
}

// trackTermination subscribes to a handler's terminal event so the exit code
// survives even if no client is attached when the process exits. On exit it
// caches the EndEvent (retained for terminatedRetentionTTL) and removes the
// process from the live map. This is what lets a Connect issued after a
// live-upgrade handover gap still return the exit code.
//
// It must be called once, right after the handler is registered in
// s.processes (both the fresh-Start and the readopt paths).
func (s *Service) trackTermination(pid uint32, proc *handler.Handler) {
	endCh, cancel := proc.EndEvent.Fork()

	go func() {
		defer cancel()

		ev, ok := <-endCh
		var end *rpc.ProcessEvent_EndEvent
		if ok {
			end = ev.End
		}
		s.finalizeTermination(pid, proc, end)
	}()
}

// finalizeTermination caches the terminal event for the retention window and
// then removes the process from the live map, guarded against PID reuse. It is
// shared by the asynchronous live-exit watcher (trackTermination) and the
// synchronous re-adopt reaper hook (Handler.OnExit). The exit is cached before
// the process is removed so a racing Connect that still finds it live can fall
// back to the cache instead of blocking on an EndEvent that will never re-fire.
func (s *Service) finalizeTermination(pid uint32, proc *handler.Handler, end *rpc.ProcessEvent_EndEvent) {
	// If the PID was already reused by a newer process, this exit is not ours to
	// cache or evict — leave the successor untouched.
	if cur, live := s.processes.Load(pid); !live || cur != proc {
		return
	}
	if end != nil {
		s.retain(pid, &retainedExit{
			pid:    pid,
			tag:    proc.Tag,
			end:    end,
			expiry: time.Now().Add(terminatedRetentionTTL),
		})
	}

	s.processes.CompareAndDelete(pid, proc)
}

// retain stores a terminal event in the retention cache and schedules its
// eviction at r.expiry. Shared by the live exit path (trackTermination) and the
// live-upgrade handover restore, so a process that exited shortly before the
// upgrade still has its exit code available on the new envd.
func (s *Service) retain(pid uint32, r *retainedExit) {
	d := time.Until(r.expiry)
	if d <= 0 {
		return
	}

	s.terminated.Store(pid, r)
	time.AfterFunc(d, func() {
		// Only evict this entry: a later retain for the same PID replaces it and
		// arms its own timer, so this stale timer must not delete the newer one.
		s.terminated.CompareAndDelete(pid, r)
	})
}

func Handle(server *chi.Mux, l *zerolog.Logger, defaults *execcontext.Defaults, workloadFreezer *cgroups.WorkloadFreezer) *Service {
	service := newService(l, defaults, workloadFreezer)

	interceptors := connect.WithInterceptors(logs.NewUnaryLogInterceptor(l))

	path, h := spec.NewProcessHandler(service, interceptors)

	server.Mount(path, h)

	return service
}

func (s *Service) getProcess(selector *rpc.ProcessSelector) (*handler.Handler, error) {
	var proc *handler.Handler

	switch selector.GetSelector().(type) {
	case *rpc.ProcessSelector_Pid:
		p, ok := s.processes.Load(selector.GetPid())
		if !ok {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("process with pid %d not found", selector.GetPid()))
		}

		proc = p
	case *rpc.ProcessSelector_Tag:
		tag := selector.GetTag()

		s.processes.Range(func(_ uint32, value *handler.Handler) bool {
			if value.Tag == nil {
				return true
			}

			if *value.Tag == tag {
				proc = value

				return true
			}

			return false
		})

		if proc == nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("process with tag %s not found", tag))
		}

	default:
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("invalid input type %T", selector))
	}

	return proc, nil
}
