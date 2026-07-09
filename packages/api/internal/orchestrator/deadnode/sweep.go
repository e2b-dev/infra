package deadnode

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/orchestrator/deadnode")

const (
	// sweepInterval is the tick interval.
	sweepInterval = time.Minute

	// backstopTicks forces a full scan every Nth tick
	backstopTicks = 5

	// gracePeriod is how long a node must go unseen by EVERY API
	// replica before its sandboxes are purged from the store. Liveness is a
	// cross-replica signal, so a single replica with a broken network
	// cannot trigger a purge
	gracePeriod = 5 * time.Minute

	// killTimeout bounds a single sandbox removal so one stuck kill
	// cannot stall the whole sweep.
	killTimeout = 30 * time.Second

	// killConcurrency bounds parallel removals per sweep cycle.
	killConcurrency = 16

	// discoveryFreshnessMax is how stale the last successful node discovery may
	// be before the sweep refuses to run
	discoveryFreshnessMax = 30 * time.Second
)

// Sweeper removes store records for sandboxes whose node crashed.
// A node is considered dead only when no API replica has completed a
// successful sync with it for gracePeriod
type Sweeper struct {
	flags       *featureflags.Client
	redisClient redis.UniversalClient

	lastDiscoverySync func() time.Time
	listSandboxes     func(ctx context.Context) ([]sandbox.Sandbox, error)
	// anyNodeUnreachablePast reports whether any pool node has been locally
	// unreachable for at least the given duration
	anyNodeUnreachablePast func(d time.Duration) bool
	removeSandbox          func(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) error

	tick int
}

func New(
	flags *featureflags.Client,
	redisClient redis.UniversalClient,
	listSandboxes func(ctx context.Context) ([]sandbox.Sandbox, error),
	lastDiscoverySync func() time.Time,
	anyNodeUnreachablePast func(d time.Duration) bool,
	removeSandbox func(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) error,
) *Sweeper {
	return &Sweeper{
		flags:                  flags,
		redisClient:            redisClient,
		listSandboxes:          listSandboxes,
		lastDiscoverySync:      lastDiscoverySync,
		anyNodeUnreachablePast: anyNodeUnreachablePast,
		removeSandbox:          removeSandbox,
	}
}

// Start runs the sweep loop until the context is cancelled.
func (s *Sweeper) Start(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.L().Info(ctx, "Stopping dead node sweep")

			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

func (s *Sweeper) sweep(ctx context.Context) {
	if !s.flags.BoolFlag(ctx, featureflags.DeadNodeSweepEnabledFlag) {
		return
	}

	last := s.lastDiscoverySync()
	now := time.Now()
	if last.IsZero() || now.Sub(last) > discoveryFreshnessMax {
		logger.L().Warn(ctx, "Dead node sweep: node discovery is stale, skipping cycle",
			logger.Time("last_discovery_sync", last),
		)

		return
	}

	// Skip the store scan unless a purge candidate might exist.
	// Nodes absent from this replica's pool entirely are only discoverable by scanning,
	// hence the periodic unconditional backstop tick.
	s.tick++
	backstop := s.tick%backstopTicks == 0
	if !backstop && !s.anyNodeUnreachablePast(gracePeriod) {
		return
	}

	ctx, span := tracer.Start(ctx, "dead-node-sweep")
	defer span.End()

	sandboxes, err := s.listSandboxes(ctx)
	if err != nil {
		logger.L().Error(ctx, "Dead node sweep: failed to list stored sandboxes, skipping cycle", zap.Error(err))

		return
	}
	if len(sandboxes) == 0 {
		return
	}

	refs := uniqueNodeRefs(sandboxes)
	liveness, err := fetchNodeLiveness(ctx, s.redisClient, refs, time.Now())
	if err != nil {
		logger.L().Error(ctx, "Dead node sweep: failed to fetch node liveness, skipping cycle", zap.Error(err))

		return
	}

	toKill := collectDeadNodeSandboxes(now, sandboxes, liveness)
	if len(toKill) == 0 {
		return
	}

	logger.L().Warn(ctx, "Dead node sweep: removing sandboxes whose node is gone",
		zap.Int("sandbox_count", len(toKill)),
	)

	sem := make(chan struct{}, killConcurrency)
	var wg sync.WaitGroup
	for _, sbx := range toKill {
		sem <- struct{}{}
		wg.Add(1)
		go func(sbx sandbox.Sandbox) {
			defer wg.Done()
			defer func() { <-sem }()

			s.kill(ctx, sbx)
		}(sbx)
	}
	wg.Wait()
}

func (s *Sweeper) kill(ctx context.Context, sbx sandbox.Sandbox) {
	killCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), killTimeout)
	defer cancel()

	// nil covers the expected outcomes too (already removed elsewhere, node
	// kill RPC failing after store cleanup) — the injected removeSandbox owns
	// that translation.
	err := s.removeSandbox(killCtx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{
		Action: sandbox.StateActionKill,
		Reason: sandbox.KillReasonNodeGone,
	})
	if err != nil {
		logger.L().Error(ctx, "Dead node sweep: failed to remove sandbox",
			zap.Error(err),
			logger.WithSandboxID(sbx.SandboxID),
			logger.WithNodeID(sbx.NodeID),
		)
	}
}

func uniqueNodeRefs(sandboxes []sandbox.Sandbox) []NodeRef {
	seen := make(map[NodeRef]struct{})
	var refs []NodeRef
	for _, sbx := range sandboxes {
		ref := NodeRef{ClusterID: sbx.ClusterID.String(), NodeID: sbx.NodeID}
		if _, ok := seen[ref]; ok {
			continue
		}

		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}

	return refs
}

// collectDeadNodeSandboxes returns the sandboxes whose node no replica has
// seen for gracePeriod:
func collectDeadNodeSandboxes(
	now time.Time,
	sandboxes []sandbox.Sandbox,
	liveness map[NodeRef]nodeLiveness,
) []sandbox.Sandbox {
	var toKill []sandbox.Sandbox

	for _, sbx := range sandboxes {
		ref := NodeRef{ClusterID: sbx.ClusterID.String(), NodeID: sbx.NodeID}

		evidence, ok := liveness[ref]
		if !ok {
			// No evidence about this node: never destructive on missing data.
			continue
		}

		switch {
		case !evidence.lastSeen.IsZero():
			if now.Sub(evidence.lastSeen) < gracePeriod {
				continue
			}
		case !evidence.firstMissing.IsZero():
			if now.Sub(evidence.firstMissing) < gracePeriod {
				continue
			}
		default:
			continue
		}

		toKill = append(toKill, sbx)
	}

	return toKill
}
