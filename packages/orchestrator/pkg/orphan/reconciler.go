//go:build linux

package orphan

import (
	"context"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	// defaultMinOrphanAge is the minimum age a PPID-1 Firecracker process must
	// have before it is considered an orphan eligible for cleanup.
	// Set to 24 hours so that processes started today are never touched.
	defaultMinOrphanAge = 24 * time.Hour

	// sweepTime is the time of day (local time) at which the daily sweep runs.
	sweepTime = 18*time.Hour + 20*time.Minute
)

// Config holds tunable parameters for the Reconciler.
type Config struct {
	// TmpDirs is the list of directories to scan for fc-*.sock and
	// fc-metrics-*.fifo files.  Defaults to [os.TempDir(), "/data0/tmp"].
	TmpDirs []string

	// MinOrphanAge is the minimum age a PPID-1 Firecracker process must have
	// before it is eligible for cleanup.  Defaults to 24 hours.
	MinOrphanAge time.Duration

	// DryRun controls whether the reconciler actually kills processes and
	// removes files/interfaces.  When true it only logs what it would do.
	DryRun bool
}

func (c *Config) setDefaults() {
	if len(c.TmpDirs) == 0 {
		c.TmpDirs = []string{os.TempDir(), "/data0/tmp"}
	}

	if c.MinOrphanAge == 0 {
		c.MinOrphanAge = defaultMinOrphanAge
	}
}

// Reconciler is a long-running service that performs a daily sweep at 18:20
// local time (Beijing time), detecting and reclaiming orphaned Firecracker resources.
//
// It only targets Firecracker processes whose PPID is 1 (adopted by init) and
// that have been running for at least MinOrphanAge.  Processes still parented
// by the orchestrator itself are never touched.
type Reconciler struct {
	cfg             Config
	sandboxes       *sandbox.Map
	orchestratorPID int32
	stop            chan struct{}
}

// NewReconciler creates a new Reconciler.  Call Start to begin the background
// sweep loop.
func NewReconciler(cfg Config, sandboxes *sandbox.Map) *Reconciler {
	cfg.setDefaults()

	return &Reconciler{
		cfg:             cfg,
		sandboxes:       sandboxes,
		orchestratorPID: int32(os.Getpid()),
		stop:            make(chan struct{}),
	}
}

// Start blocks until ctx is cancelled or Stop is called, running a sweep once
// per day at the configured sweepTime (18:20 local time by default).
func (r *Reconciler) Start(ctx context.Context) error {
	logger.L().Info(ctx, "orphan reconciler: started",
		zap.Int32("orchestrator_pid", r.orchestratorPID),
		zap.Duration("min_orphan_age", r.cfg.MinOrphanAge),
		zap.Bool("dry_run", r.cfg.DryRun),
		zap.Strings("tmp_dirs", r.cfg.TmpDirs),
		zap.Duration("sweep_time", sweepTime),
	)

	for {
		next := nextSweepTime(time.Now(), sweepTime)

		logger.L().Info(ctx, "orphan reconciler: next sweep scheduled",
			zap.Time("at", next),
			zap.Duration("in", time.Until(next)),
		)

		select {
		case <-ctx.Done():
			logger.L().Info(ctx, "orphan reconciler: context cancelled, stopping")

			return nil

		case <-r.stop:
			logger.L().Info(ctx, "orphan reconciler: stop requested")

			return nil

		case <-time.After(time.Until(next)):
			r.runSweep(ctx)
		}
	}
}

// Stop signals the reconciler to exit after the current sweep (if any)
// completes.
func (r *Reconciler) Stop() {
	select {
	case r.stop <- struct{}{}:
	default:
	}
}

// Close implements the closer interface used by factories/run.go.
func (r *Reconciler) Close(_ context.Context) error {
	r.Stop()

	return nil
}

// runSweep performs a single full reconciliation pass and logs the results.
func (r *Reconciler) runSweep(ctx context.Context) {
	logger.L().Info(ctx, "orphan reconciler: sweep started",
		zap.Bool("dry_run", r.cfg.DryRun),
	)

	start := time.Now()

	result, err := r.sweep(ctx)
	if err != nil {
		logger.L().Error(ctx, "orphan reconciler: sweep failed", zap.Error(err))

		return
	}

	elapsed := time.Since(start)

	if result.IsClean() {
		logger.L().Info(ctx, "orphan reconciler: sweep complete — host is clean",
			zap.Duration("elapsed", elapsed),
		)

		return
	}

	logger.L().Warn(ctx, "orphan reconciler: sweep complete — orphans found",
		zap.Int("total", result.Total()),
		zap.Int("processes", len(result.OrphanedProcesses)),
		zap.Int("sockets", len(result.OrphanedSockets)),
		zap.Int("fifos", len(result.OrphanedFIFOs)),
		zap.Int("veths", len(result.OrphanedVeths)),
		zap.Duration("elapsed", elapsed),
		zap.Bool("dry_run", r.cfg.DryRun),
	)

	if r.cfg.DryRun {
		r.logDryRunDetails(ctx, result)

		return
	}

	r.clean(ctx, result)
}

// sweep scans the host for orphaned resources and returns a SweepResult.
func (r *Reconciler) sweep(ctx context.Context) (*SweepResult, error) {
	// Build the set of socket paths currently held open by any live FC process
	// (regardless of PPID) so we can skip sockets that are still in use.
	liveSockets, err := buildLiveSockets()
	if err != nil {
		return nil, err
	}

	// Build the set of slot indices that are currently live in the sandbox map.
	liveSlotIdxs := r.liveSlotIdxs()

	orphanedProcs, err := scanOrphanedProcesses(r.orchestratorPID, r.cfg.MinOrphanAge)
	if err != nil {
		return nil, err
	}

	orphanedSockets, err := scanOrphanedSockets(r.cfg.TmpDirs, liveSockets)
	if err != nil {
		return nil, err
	}

	orphanedFIFOs, err := scanOrphanedFIFOs(r.cfg.TmpDirs, liveSockets)
	if err != nil {
		return nil, err
	}

	orphanedVeths, err := scanOrphanedVeths(liveSlotIdxs)
	if err != nil {
		// Non-fatal: log and continue without veth cleanup.
		logger.L().Error(ctx, "orphan reconciler: failed to scan veths", zap.Error(err))
		orphanedVeths = nil
	}

	return &SweepResult{
		OrphanedProcesses: orphanedProcs,
		OrphanedSockets:   orphanedSockets,
		OrphanedFIFOs:     orphanedFIFOs,
		OrphanedVeths:     orphanedVeths,
	}, nil
}

// clean performs the actual cleanup of all orphaned resources found in result.
func (r *Reconciler) clean(ctx context.Context, result *SweepResult) {
	// 1. Kill orphaned processes first so they release their sockets/FIFOs.
	if len(result.OrphanedProcesses) > 0 {
		pr := cleanOrphanedProcesses(ctx, result.OrphanedProcesses)
		logCleanResult(ctx, "processes", pr)
	}

	// 2. Remove leftover socket files.
	if len(result.OrphanedSockets) > 0 {
		sr := cleanOrphanedSockets(ctx, result.OrphanedSockets)
		logCleanResult(ctx, "sockets", sr)
	}

	// 3. Remove leftover FIFO files.
	if len(result.OrphanedFIFOs) > 0 {
		fr := cleanOrphanedFIFOs(ctx, result.OrphanedFIFOs)
		logCleanResult(ctx, "fifos", fr)
	}

	// 4. Remove orphaned veth interfaces and their iptables rules.
	if len(result.OrphanedVeths) > 0 {
		vr := cleanOrphanedVeths(ctx, result.OrphanedVeths)
		logCleanResult(ctx, "veths", vr)
	}
}

// liveSlotIdxs returns the set of network slot indices currently in use by
// live sandboxes.
func (r *Reconciler) liveSlotIdxs() map[int]struct{} {
	idxs := make(map[int]struct{})

	for _, sbx := range r.sandboxes.Items() {
		if sbx.Slot != nil {
			idxs[sbx.Slot.Idx] = struct{}{}
		}
	}

	return idxs
}

// logDryRunDetails logs the details of what would be cleaned in dry-run mode.
func (r *Reconciler) logDryRunDetails(ctx context.Context, result *SweepResult) {
	for _, p := range result.OrphanedProcesses {
		logger.L().Info(ctx, "orphan reconciler [dry-run]: would kill process",
			zap.Int32("pid", p.PID),
			zap.Int32("ppid", p.PPID),
			zap.String("socket", p.SocketPath),
			zap.Time("detected_at", p.DetectedAt),
		)
	}

	for _, s := range result.OrphanedSockets {
		logger.L().Info(ctx, "orphan reconciler [dry-run]: would remove socket",
			zap.String("path", s.Path),
		)
	}

	for _, f := range result.OrphanedFIFOs {
		logger.L().Info(ctx, "orphan reconciler [dry-run]: would remove FIFO",
			zap.String("path", f.Path),
		)
	}

	for _, v := range result.OrphanedVeths {
		logger.L().Info(ctx, "orphan reconciler [dry-run]: would delete veth + iptables rules",
			zap.String("veth", v.Name),
			zap.Int("slot_idx", v.SlotIdx),
		)
	}
}

// logCleanResult logs a summary of a single cleanup step.
func logCleanResult(ctx context.Context, kind string, r CleanResult) {
	fields := []zap.Field{zap.String("kind", kind)}

	switch kind {
	case "processes":
		fields = append(fields, zap.Int32s("killed_pids", r.KilledPIDs))
	case "sockets":
		fields = append(fields, zap.Strings("removed", r.RemovedSockets))
	case "fifos":
		fields = append(fields, zap.Strings("removed", r.RemovedFIFOs))
	case "veths":
		fields = append(fields, zap.Strings("removed", r.RemovedVeths))
	}

	if len(r.Errors) > 0 {
		errstrs := make([]string, len(r.Errors))
		for i, e := range r.Errors {
			errstrs[i] = e.Error()
		}

		fields = append(fields, zap.Strings("errors", errstrs))
		logger.L().Error(ctx, "orphan reconciler: cleanup step completed with errors", fields...)
	} else {
		logger.L().Info(ctx, "orphan reconciler: cleanup step completed", fields...)
	}
}

// nextSweepTime returns the next wall-clock time at which the sweep should run.
// If the target time has already passed today, it returns tomorrow's occurrence.
func nextSweepTime(now time.Time, sweepDuration time.Duration) time.Time {
	// Extract hour and minute from the duration.
	hour := int(sweepDuration.Hours())
	minute := int((sweepDuration % time.Hour).Minutes())

	// Truncate to the start of today in local time, then add the target time.
	y, m, d := now.Date()
	loc := now.Location()
	candidate := time.Date(y, m, d, hour, minute, 0, 0, loc)

	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}

	return candidate
}
