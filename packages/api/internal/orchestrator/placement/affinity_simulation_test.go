package placement

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement/affinity"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

const (
	simNodes      = 24
	simBuilds     = 120
	simPlacements = 3000
	// simCacheSize models a finite node-local build cache (LRU); without it the
	// popular builds end up resident everywhere and affinity has nothing to win.
	simCacheSize = 12
)

// simCache is a tiny per-node LRU keyed by build ID, valued by last-use index.
type simCache map[string]int

func (c simCache) touch(build string, now int) {
	c[build] = now
	if len(c) <= simCacheSize {
		return
	}
	oldest, oldestAt := "", now+1
	for b, at := range c {
		if at < oldestAt {
			oldest, oldestAt = b, at
		}
	}
	delete(c, oldest)
}

// simWorkload generates a fixed, zipf-skewed stream of build IDs so the
// baseline and affinity runs replay the identical request sequence.
func simWorkload() []string {
	rng := rand.New(rand.NewSource(42))
	zipf := rand.NewZipf(rng, 1.1, 2, simBuilds-1)

	builds := make([]string, simPlacements)
	for i := range builds {
		builds[i] = fmt.Sprintf("build-%02d", zipf.Uint64())
	}

	return builds
}

// runPlacementSimulation replays the workload against a fresh fleet, using the
// cache-affinity bonus when idx is non-nil. It returns the node-local cache
// hit rate and per-node placement counts.
func runPlacementSimulation(t *testing.T, builds []string, idx *affinity.Index, cfg affinity.Config) (float64, map[string]int) {
	t.Helper()
	ctx := t.Context()
	cluster := uuid.New()

	algo := NewBestOfK(BestOfKConfig{R: 4, Alpha: 0.5, K: 3}).(*BestOfK)
	nodes := make([]*nodemanager.Node, simNodes)
	for i := range nodes {
		nodes[i] = nodemanager.NewTestNode(fmt.Sprintf("node-%02d", i), api.NodeStatusReady, 0, 64)
	}

	resources := nodemanager.SandboxResources{CPUs: 2, MiBMemory: 1024}
	nodeCache := make(map[string]simCache, simNodes)
	placements := make(map[string]int, simNodes)

	hits := 0
	for i, build := range builds {
		var scores map[string]float64
		if idx != nil {
			scores = idx.Scores(ctx, cfg, cluster, build)
		}

		node, err := algo.chooseNode(ctx, nodes, nil, resources, machineinfo.MachineInfo{}, false, nil, scores)
		require.NoError(t, err)

		if _, ok := nodeCache[node.ID][build]; ok {
			hits++
		}

		if idx != nil {
			idx.Record(ctx, cfg, cluster, node.ID, &orchestrator.SchedulingMetadata{BuildId: build})
		}
		if nodeCache[node.ID] == nil {
			nodeCache[node.ID] = make(simCache, simCacheSize)
		}
		nodeCache[node.ID].touch(build, i)
		placements[node.ID]++

		// Leave the sandbox "running" so load accumulates and the load term keeps
		// counteracting affinity concentration.
		node.PlacementMetrics.StartPlacing(fmt.Sprintf("sbx-%d", i), resources)
	}

	return float64(hits) / float64(len(builds)), placements
}

// Replays the same workload with and without the affinity bonus: affinity must
// improve node-local build reuse while keeping load distribution bounded.
func TestSimulation_AffinityImprovesCacheHitsWithoutSkew(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("simulation needs a redis container")
	}

	idx := affinity.NewIndex(redis_utils.SetupInstance(t))
	cfg := affinity.Config{
		Enabled:            true,
		TTL:                time.Hour,
		TopNodes:           20,
		ReadTimeout:        time.Second,
		WriteTimeout:       time.Second,
		MaxHits:            10,
		ExactWeight:        0.002,
		BaseWeight:         0.001,
		LineageWeight:      0.0005,
		MaxBonus:           0.02,
		MaxLineageRecorded: 16,
	}

	builds := simWorkload()
	baselineHits, baselinePlacements := runPlacementSimulation(t, builds, nil, cfg)
	affinityHits, affinityPlacements := runPlacementSimulation(t, builds, idx, cfg)

	t.Logf("cache hit rate: baseline=%.3f affinity=%.3f", baselineHits, affinityHits)
	t.Logf("placements per node: baseline=%v affinity=%v", baselinePlacements, affinityPlacements)

	assert.Greater(t, affinityHits, baselineHits+0.03, "affinity should clearly improve node-local build reuse")

	mean := float64(simPlacements) / float64(simNodes)
	for nodeID, count := range affinityPlacements {
		assert.LessOrEqualf(t, float64(count), 3*mean, "node %s is over-concentrated", nodeID)
	}
}
