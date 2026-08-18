//go:build linux

package uffd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd")

const (
	uffdMsgListenerTimeout = 10 * time.Second
	fdSize                 = 4
	regionMappingsSize     = 1024
)

type Uffd struct {
	exit       *utils.ErrorOnce
	readyCh    chan struct{}
	readyOnce  sync.Once
	lis        *net.UnixListener
	socketPath string
	memfile    block.ReadonlyDevice
	memfd      atomic.Pointer[block.Memfd]
	handler    utils.SetOnce[*userfaultfd.Userfaultfd]
	fdExit     utils.SetOnce[*fdexit.FdExit]

	// syncWP records whether the paired Firecracker was resumed with
	// use_sync_wp (synchronous WP fault delivery). Set once at resume from
	// the same decision passed to fc.Resume; the backend owns its mode so
	// DiffMetadata can refuse the tracker dirty source for a WP_ASYNC
	// sandbox instead of trusting the caller's comment-enforced precondition.
	syncWP atomic.Bool
}

var _ MemoryBackend = (*Uffd)(nil)

func New(memfile block.ReadonlyDevice, socketPath string) *Uffd {
	return &Uffd{
		exit:       utils.NewErrorOnce(),
		readyCh:    make(chan struct{}),
		socketPath: socketPath,
		memfile:    memfile,
		handler:    *utils.NewSetOnce[*userfaultfd.Userfaultfd](),
		fdExit:     *utils.NewSetOnce[*fdexit.FdExit](),
	}
}

// SetSyncWP records the sandbox's write-protect delivery mode; see the field.
func (u *Uffd) SetSyncWP(v bool) {
	u.syncWP.Store(v)
}

func (u *Uffd) Prefault(ctx context.Context, offset int64, data []byte) (installed bool, e error) {
	handler, err := u.handler.WaitWithContext(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get uffd: %w", err)
	}

	return handler.Prefault(ctx, offset, data)
}

func (u *Uffd) Start(ctx context.Context, sandboxId string) error {
	lis, err := net.ListenUnix("unix", &net.UnixAddr{Name: u.socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("failed listening on socket: %w", err)
	}

	u.lis = lis

	err = os.Chmod(u.socketPath, 0o777)
	if err != nil {
		closeErr := lis.Close()

		return fmt.Errorf("failed setting socket permissions: %w", errors.Join(err, closeErr))
	}

	fdExit, err := fdexit.New()
	if err != nil {
		closeErr := lis.Close()

		return fmt.Errorf("failed to create fd exit: %w", errors.Join(err, closeErr))
	}

	u.fdExit.SetValue(fdExit)

	go func() {
		ctx, span := tracer.Start(ctx, "serve uffd")
		defer span.End()

		handleErr := u.handle(ctx, sandboxId, fdExit)

		// If handle failed before setting the handler value, set an error to unblock
		// any waiters (e.g., prefetcher goroutines waiting on Prefault).
		if handleErr != nil {
			u.handler.SetError(handleErr)
			logger.L().Error(ctx, "uffd handle failed, sandbox will be terminated",
				logger.WithSandboxID(sandboxId),
				zap.String("socket_path", u.socketPath),
				zap.Error(handleErr))
		}

		closeErr := u.lis.Close()
		fdExitErr := fdExit.Close()

		u.exit.SetError(errors.Join(handleErr, closeErr, fdExitErr))

		// Close the ready channel to unblock any waiters (safe to call multiple times via Once)
		u.readyOnce.Do(func() { close(u.readyCh) })
	}()

	return nil
}

func (u *Uffd) handle(ctx context.Context, sandboxId string, fdExit *fdexit.FdExit) error {
	err := u.lis.SetDeadline(time.Now().Add(uffdMsgListenerTimeout))
	if err != nil {
		return fmt.Errorf("failed setting listener deadline: %w", err)
	}

	conn, err := u.lis.Accept()
	if err != nil {
		return fmt.Errorf("failed accepting firecracker connection: %w", err)
	}

	unixConn := conn.(*net.UnixConn)

	regionMappingsBuf := make([]byte, regionMappingsSize)
	// Firecracker may send 1 fd (UFFD) or 2 (UFFD + memfd, on newer versions).
	fdBuf := make([]byte, syscall.CmsgSpace(2*fdSize))

	numBytesMappings, numBytesFd, _, _, err := unixConn.ReadMsgUnix(regionMappingsBuf, fdBuf)
	if err != nil {
		return fmt.Errorf("failed to read unix msg from connection: %w", err)
	}

	regionMappingsBuf = regionMappingsBuf[:numBytesMappings]

	var regions []memory.Region

	err = json.Unmarshal(regionMappingsBuf, &regions)
	if err != nil {
		return fmt.Errorf("failed parsing memory mapping data: %w", err)
	}

	controlMsgs, err := syscall.ParseSocketControlMessage(fdBuf[:numBytesFd])
	if err != nil {
		return fmt.Errorf("failed parsing control messages: %w", err)
	}

	if len(controlMsgs) != 1 {
		return fmt.Errorf("expected 1 control message containing UFFD and (maybe) memfd: found %d", len(controlMsgs))
	}

	fds, err := syscall.ParseUnixRights(&controlMsgs[0])
	if err != nil {
		return fmt.Errorf("failed parsing unix write: %w", err)
	}

	if len(fds) == 0 {
		return errors.New("expected at least 1 file descriptor")
	}

	m := memory.NewMapping(regions)

	// The memfile header's generation (pause/resume cycle count) tags this
	// sandbox's fault metrics so latency can be cut by snapshot chain depth.
	var generation uint64
	if h := u.memfile.Header(); h != nil && h.Metadata != nil {
		generation = h.Metadata.Generation
	}

	uffd, err := userfaultfd.NewUserfaultfdFromFd(
		uintptr(fds[0]),
		u.memfile,
		m,
		generation,
		logger.L().With(logger.WithSandboxID(sandboxId)),
	)
	if err != nil {
		for _, fd := range fds {
			_ = syscall.Close(fd)
		}

		return fmt.Errorf("failed to create uffd: %w", err)
	}

	defer func() {
		closeErr := uffd.Close()
		if closeErr != nil {
			logger.L().Error(ctx, "failed to close uffd", logger.WithSandboxID(sandboxId), zap.String("socket_path", u.socketPath), zap.Error(closeErr))
		}

		if m := u.memfd.Swap(nil); m != nil {
			if closeErr := m.Close(); closeErr != nil {
				logger.L().Error(ctx, "failed to close memfd", logger.WithSandboxID(sandboxId), zap.Error(closeErr))
			}
		}
	}()

	if len(fds) > 1 {
		memfd, err := block.NewFromFd(fds[1])
		if err != nil {
			return fmt.Errorf("failed to wrap memfd: %w", err)
		}
		u.memfd.Store(memfd)
	}

	u.handler.SetValue(uffd)

	u.readyOnce.Do(func() { close(u.readyCh) })

	err = uffd.Serve(
		ctx,
		fdExit,
	)
	if err != nil {
		return fmt.Errorf("failed handling uffd: %w", err)
	}

	return nil
}

func (u *Uffd) Stop() error {
	fdExit, err := u.fdExit.Result()
	if err != nil {
		return fmt.Errorf("fdExit not set or failed: %w", err)
	}

	return fdExit.SignalExit()
}

func (u *Uffd) Ready() chan struct{} {
	return u.readyCh
}

func (u *Uffd) Exit() *utils.ErrorOnce {
	return u.exit
}

// DiffMetadata waits for the current requests to finish and returns the dirty pages.
//
// It *MUST* be only called after the sandbox was successfully paused via API and after the snapshot endpoint was called.
//
// With useTrackerDirty set (sync-WP sandboxes only, behind the
// sync-wp-tracker-dirty kill switch) the dirty and empty sets come entirely
// from the page tracker — installs plus synchronous WP-fault promotions —
// and Firecracker's GetDirtyMemory pagemap scan is skipped. Otherwise the
// pagemap RPC stays the dirty source and the tracker view is only compared
// against it (the divergence telemetry below: the dirty_divergence metric
// and the per-pause log line), which doubles as the burn-in gate for
// enabling the tracker source: pagemap_only must be zero for sync-WP
// sandboxes, since a page only the pagemap sees dirty is a write the tracker
// missed (snapshot corruption if the RPC were dropped). Flag off is thus the
// dual-source shadow mode: both views are computed each pause and compared,
// with the pagemap staying authoritative.
func (u *Uffd) DiffMetadata(ctx context.Context, f *fc.Process, useTrackerDirty bool) (*header.DiffMetadata, error) {
	handler, err := u.handler.WaitWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get uffd: %w", err)
	}

	// Settle in-flight UFFD workers (WP resolves and the REMOVE batch
	// included) before reading the dirty set, so a Zero→Write install or a
	// pending promotion can't slip in after the readout. The vCPUs are
	// paused, so no new faults arrive after the drain.
	faulted, empty := handler.ExportPageStates()

	// The build this sandbox resumed FROM, to correlate divergence readings
	// with a template/build without a Loki join.
	var buildID string
	if u.memfile != nil {
		if h := u.memfile.Header(); h != nil && h.Metadata != nil {
			buildID = h.Metadata.BuildId.String()
		}
	}

	if useTrackerDirty {
		// Fail closed: under WP_ASYNC no WP events reach the serve loop, so
		// the tracker misses every post-install guest write — a caller bug
		// here must be a loud pause failure, not a silently corrupt snapshot.
		if !u.syncWP.Load() {
			return nil, errors.New("tracker dirty source requested for a sandbox without sync-WP fault delivery")
		}

		// The tracker state bitmaps are disjoint by invariant, so the empty
		// set (zero ∪ removed) never intersects the dirty set. Enforce it
		// anyway: downstream MergeMappings lets EMPTY win on overlap, which
		// would restore a written page as zeros — if the invariant ever
		// breaks, dirty must win (mirrors the pagemap branch below).
		empty.AndNot(faulted)

		handler.Logger().Info(ctx, "dirty source: page tracker (sync-WP)",
			zap.String("build_id", buildID),
			zap.Uint64("tracker_dirty_pages", faulted.GetCardinality()),
			zap.Uint64("tracker_empty_pages", empty.GetCardinality()))

		return &header.DiffMetadata{
			BlockSize: handler.PageSize(),
			Dirty:     faulted,
			Empty:     empty,
		}, nil
	}

	diff, err := f.DirtyMemory(ctx, handler.PageSize())
	if err != nil {
		return nil, fmt.Errorf("failed to get dirty memory: %w", err)
	}

	// Dirty-source divergence telemetry: compare the page tracker's view with
	// the pagemap readout, as burn-in evidence for the tracker source above.
	//   tracker_only: tracker Dirty, pagemap clean — should converge to zero
	//     now that MODE_WP installs are recorded Clean instead of Dirty.
	//   pagemap_only: pagemap dirty, tracker unaware — expected under WP_ASYNC
	//     (in-kernel WP clears deliver no event); must be zero under sync-WP,
	//     where every clear passes through the serve loop and promotes.
	// Gated on the sandbox's MODE, not on WPFaultsResolved() > 0: a sync-WP
	// sandbox whose WP delivery silently broke resolves zero faults and has
	// massive pagemap_only divergence — exactly the case the burn-in exists
	// to catch, and exactly the case a fault-count gate would suppress.
	// Under WP_ASYNC the pagemap diverges on every pause by design, so
	// those sandboxes stay silent.
	if u.syncWP.Load() {
		trackerOnly, pagemapOnly, pagemapDirty := divergenceCardinalities(faulted, diff.Dirty)
		dirtyDivergencePages.Record(ctx, int64(trackerOnly), dirtyDivergenceAttrs["tracker_only"])
		dirtyDivergencePages.Record(ctx, int64(pagemapOnly), dirtyDivergenceAttrs["pagemap_only"])
		dirtyDivergencePages.Record(ctx, int64(pagemapDirty), dirtyDivergenceAttrs["pagemap_dirty"])
		handler.Logger().Info(ctx, "dirty-source divergence (tracker vs pagemap)",
			zap.String("build_id", buildID),
			zap.Uint64("tracker_only_pages", trackerOnly),
			zap.Uint64("pagemap_only_pages", pagemapOnly),
			zap.Uint64("tracker_dirty_pages", faulted.GetCardinality()),
			zap.Uint64("pagemap_dirty_pages", pagemapDirty),
			zap.Int64("wp_faults_resolved", handler.WPFaultsResolved()))
	}

	// Pages that were zero-installed and later written show up in diff.Dirty —
	// via the in-kernel WP-async clear or the synchronous WP-fault resolve —
	// so dirty wins over empty for those.
	empty.AndNot(diff.Dirty)

	return &header.DiffMetadata{
		BlockSize: diff.BlockSize,
		Dirty:     diff.Dirty,
		Empty:     empty,
	}, nil
}

// PrefetchData returns page fault data for prefetch mapping.
func (u *Uffd) PrefetchData(ctx context.Context) (block.PrefetchData, error) {
	uffd, err := u.handler.WaitWithContext(ctx)
	if err != nil {
		return block.PrefetchData{}, fmt.Errorf("failed to get uffd: %w", err)
	}

	return uffd.PrefetchData(), nil
}

// Memfd returns the memfd received from Firecracker and transfers ownership to
// the caller. The uffd teardown defer will no longer close it.
func (u *Uffd) Memfd(_ context.Context) *block.Memfd {
	return u.memfd.Swap(nil)
}

// ServeStats returns a cumulative snapshot of demand faults served so far, or a
// zero snapshot if the handler has not been created yet (FC has not connected).
// It never blocks.
func (u *Uffd) ServeStats() userfaultfd.ServeSnapshot {
	handler, err := u.handler.Result()
	if err != nil {
		return userfaultfd.ServeSnapshot{}
	}

	return handler.ServeStats()
}
