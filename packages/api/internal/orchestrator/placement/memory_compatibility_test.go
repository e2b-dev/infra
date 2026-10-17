package placement

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/units"
)

var testPageBytes = uint64(units.MBToBytes(2))

// hugepageNode reports a pool of total pages, used+reserved committed, and a
// ceiling of capacity pages (base pool plus overcommit allowance).
func hugepageNode(id string, total, used, reserved, capacity uint64) *nodemanager.Node {
	return nodemanager.NewTestNode(id, api.NodeStatusReady, 0, 8,
		nodemanager.WithHugePages(total, used, reserved, testPageBytes),
		nodemanager.WithHugePagesCapacity(capacity),
	)
}

// 512 MiB is 256 pages.
var hugepageSandbox = nodemanager.SandboxResources{CPUs: 1, MiBMemory: 512, HugePages: true}

var limit95 = MemoryRequirement{Limit: 0.95}

func TestNodeSatisfiesMemory_RefusesPastTheLimit(t *testing.T) {
	t.Parallel()

	// 1800 committed + 256 requested > 0.95 * 2000.
	assert.False(t, NodeSatisfiesMemory(hugepageNode("n", 1600, 1600, 200, 2000), hugepageSandbox, limit95))
}

func TestNodeSatisfiesMemory_ExactFitAtTheLimitPasses(t *testing.T) {
	t.Parallel()

	// 1644 committed + 256 requested == 0.95 * 2000.
	assert.True(t, NodeSatisfiesMemory(hugepageNode("n", 1600, 1600, 44, 2000), hugepageSandbox, limit95))
}

// A node borrowing surplus pages has Used+Reserved == HugePages_Total; with
// overcommit allowance left it still has room.
func TestNodeSatisfiesMemory_NodeOnSurplusWithAllowanceLeftPasses(t *testing.T) {
	t.Parallel()

	assert.True(t, NodeSatisfiesMemory(hugepageNode("n", 1000, 900, 100, 1500), hugepageSandbox, limit95))
}

// A pool that is all overcommit (no base pages) grows HugePages_Total only as
// pages are borrowed, so Total always equals what is committed.
func TestNodeSatisfiesMemory_PoolThatIsAllOvercommitPasses(t *testing.T) {
	t.Parallel()

	assert.True(t, NodeSatisfiesMemory(hugepageNode("n", 300, 200, 100, 54097), hugepageSandbox, limit95))
}

func TestNodeSatisfiesMemory_CountsHugepagePlacementsInFlight(t *testing.T) {
	t.Parallel()

	node := hugepageNode("n", 1600, 1200, 0, 2000)
	assert.True(t, NodeSatisfiesMemory(node, hugepageSandbox, limit95))

	node.PlacementMetrics.StartPlacingIf("ordinary", nodemanager.SandboxResources{MiBMemory: 4096}, func() bool { return true })
	assert.True(t, NodeSatisfiesMemory(node, hugepageSandbox, limit95), "an ordinary-page placement takes nothing from the pool")

	node.PlacementMetrics.StartPlacingIf("hugepage", nodemanager.SandboxResources{MiBMemory: 1024, HugePages: true}, func() bool { return true })
	assert.False(t, NodeSatisfiesMemory(node, hugepageSandbox, limit95))
}

// A deleted sandbox may have held fewer pages than its RAM, so the node stays
// as full as last reported until the next sync instead of admitting too much.
func TestNodeSatisfiesMemory_DeleteDoesNotFreeRoomBeforeTheNextSync(t *testing.T) {
	t.Parallel()

	// 1 GiB is 512 pages; the limit is 40 GiB of a 50 GiB ceiling.
	limit := MemoryRequirement{Limit: 0.8}
	node := nodemanager.NewTestNode("n", api.NodeStatusReady, 2, 8,
		nodemanager.WithAllocatedMemoryBytes(uint64(units.MBToBytes(16*1024))),
		nodemanager.WithHugePages(19*1024, 19*1024, 0, testPageBytes),
		nodemanager.WithHugePagesCapacity(25*1024),
	)
	sixteenGiB := nodemanager.SandboxResources{CPUs: 2, MiBMemory: 16 * 1024, HugePages: true}
	eightGiB := nodemanager.SandboxResources{CPUs: 2, MiBMemory: 8 * 1024, HugePages: true}

	// Other sandboxes hold 30 GiB and the deleted one 8 GiB of its 16.
	node.OptimisticRemove(t.Context(), sixteenGiB)

	assert.False(t, NodeSatisfiesMemory(node, sixteenGiB, limit))

	node.UpdateMetricsFromSync(&orchestratorinfo.ServiceInfoResponse{
		MetricHugepagesTotal:    15 * 1024,
		MetricHugepagesUsed:     15 * 1024,
		MetricHugepageSizeBytes: testPageBytes,
		MetricHugepagesCapacity: 25 * 1024,
	}, time.Now())

	assert.False(t, NodeSatisfiesMemory(node, sixteenGiB, limit))
	assert.True(t, NodeSatisfiesMemory(node, eightGiB, limit))
}

func TestNodeSatisfiesMemory_PassesWhenTheCheckDoesNotApply(t *testing.T) {
	t.Parallel()

	full := hugepageNode("full", 2000, 2000, 0, 2000)
	assert.True(t, NodeSatisfiesMemory(full, hugepageSandbox, MemoryRequirement{}), "limit 0 turns the check off")
	assert.True(t, NodeSatisfiesMemory(full, nodemanager.SandboxResources{MiBMemory: 512}, limit95), "ordinary pages do not use the pool")
	assert.True(t, NodeSatisfiesMemory(hugepageNode("silent", 2000, 2000, 0, 0), hugepageSandbox, limit95), "a node that does not report its ceiling is not refused")
}
