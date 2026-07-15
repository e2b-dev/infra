package nodemanager

import (
	"testing"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

func TestNode_OptimisticAdd_FlagEnabled(t *testing.T) {
	t.Parallel()

	// 1. Create a LaunchDarkly test data source
	td := ldtestdata.DataSource()

	// 2. Set the feature flag under test to true
	td.Update(td.Flag(featureflags.OptimisticResourceAccountingFlag.Key()).VariationForAll(true))

	// 3. Create a Feature Flag client with the test data source
	ffClient, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)

	// 4. Initialize Node with the injected ffClient
	node := NewTestNode("test-node", api.NodeStatusReady, 0, 4, WithFeatureFlags(ffClient))
	initialMetrics := node.Metrics()

	// 5. Call the method
	res := SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}
	node.OptimisticAdd(t.Context(), res)

	// 6. Assert: When flag is enabled, resources should be successfully accumulated
	newMetrics := node.Metrics()
	assert.Equal(t, initialMetrics.CpuAllocated+uint32(res.CPUs), newMetrics.CpuAllocated)
	assert.Equal(t, initialMetrics.MemoryAllocatedBytes+uint64(res.MiBMemory)*1024*1024, newMetrics.MemoryAllocatedBytes)
}

func TestNode_OptimisticAdd_FlagDisabled(t *testing.T) {
	t.Parallel()

	// 1. Create a LaunchDarkly test data source
	td := ldtestdata.DataSource()

	// 2. Set the feature flag under test to false
	td.Update(td.Flag(featureflags.OptimisticResourceAccountingFlag.Key()).VariationForAll(false))

	// 3. Create a Feature Flag client with the test data source
	ffClient, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)

	// 4. Initialize Node with the injected ffClient
	node := NewTestNode("test-node", api.NodeStatusReady, 0, 4, WithFeatureFlags(ffClient))
	initialMetrics := node.Metrics()

	// 5. Call the method
	res := SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}
	node.OptimisticAdd(t.Context(), res)

	// 6. Assert: When flag is disabled, return early, resources should not be accumulated
	newMetrics := node.Metrics()
	assert.Equal(t, initialMetrics.CpuAllocated, newMetrics.CpuAllocated)
	assert.Equal(t, initialMetrics.MemoryAllocatedBytes, newMetrics.MemoryAllocatedBytes)
}

func TestNode_OptimisticRemove_FlagEnabled(t *testing.T) {
	t.Parallel()

	// 1. Create a LaunchDarkly test data source
	td := ldtestdata.DataSource()

	// 2. Set the feature flag under test to true
	td.Update(td.Flag(featureflags.OptimisticResourceAccountingFlag.Key()).VariationForAll(true))

	// 3. Create a Feature Flag client with the test data source
	ffClient, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)

	// 4. Initialize Node with the injected ffClient - some resources are already allocated at initialization
	node := NewTestNode("test-node", api.NodeStatusReady, 4, 8192, WithFeatureFlags(ffClient), WithAllocatedMemoryBytes(8192*1024*1024))
	initialMetrics := node.Metrics()

	// 5. Call the method
	res := SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}
	node.OptimisticRemove(t.Context(), res)

	// 6. Assert: When flag is enabled, resources should be successfully deducted
	newMetrics := node.Metrics()
	assert.Equal(t, initialMetrics.CpuAllocated-uint32(res.CPUs), newMetrics.CpuAllocated)
	assert.Equal(t, initialMetrics.MemoryAllocatedBytes-uint64(res.MiBMemory)*1024*1024, newMetrics.MemoryAllocatedBytes)
}

func TestNode_OptimisticRemove_SkipsWhenItWouldUnderflow(t *testing.T) {
	t.Parallel()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.OptimisticResourceAccountingFlag.Key()).VariationForAll(true))

	ffClient, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)

	// Node with less allocated than what will be removed: 1 CPU, 512 MiB
	node := NewTestNode("test-node", api.NodeStatusReady, 1, 8192, WithFeatureFlags(ffClient), WithAllocatedMemoryBytes(512*1024*1024))
	initialMetrics := node.Metrics()

	res := SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}
	node.OptimisticRemove(t.Context(), res)

	// Counters must never wrap to ~2^32/2^64; subtraction is skipped instead
	newMetrics := node.Metrics()
	assert.Equal(t, initialMetrics.CpuAllocated, newMetrics.CpuAllocated)
	assert.Equal(t, initialMetrics.MemoryAllocatedBytes, newMetrics.MemoryAllocatedBytes)
}

func TestNode_OptimisticRemove_FreshNodeDoesNotUnderflow(t *testing.T) {
	t.Parallel()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.OptimisticResourceAccountingFlag.Key()).VariationForAll(true))

	ffClient, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)

	// Fresh node: nothing allocated yet (e.g. poll overwrote counters after sandbox already left the orchestrator)
	node := NewTestNode("test-node", api.NodeStatusReady, 0, 8192, WithFeatureFlags(ffClient))

	node.OptimisticRemove(t.Context(), SandboxResources{CPUs: 2, MiBMemory: 1024})

	newMetrics := node.Metrics()
	assert.Equal(t, uint32(0), newMetrics.CpuAllocated)
	assert.Equal(t, uint64(0), newMetrics.MemoryAllocatedBytes)
}

func TestNode_OptimisticRemove_FlagDisabled(t *testing.T) {
	t.Parallel()

	// 1. Create a LaunchDarkly test data source
	td := ldtestdata.DataSource()

	// 2. Set the feature flag under test to false
	td.Update(td.Flag(featureflags.OptimisticResourceAccountingFlag.Key()).VariationForAll(false))

	// 3. Create a Feature Flag client with the test data source
	ffClient, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)

	// 4. Initialize Node with the injected ffClient - some resources are already allocated at initialization
	node := NewTestNode("test-node", api.NodeStatusReady, 4, 8192, WithFeatureFlags(ffClient))
	initialMetrics := node.Metrics()

	// 5. Call the method
	res := SandboxResources{
		CPUs:      2,
		MiBMemory: 1024,
	}
	node.OptimisticRemove(t.Context(), res)

	// 6. Assert: When flag is disabled, return early, resources should remain unchanged
	newMetrics := node.Metrics()
	assert.Equal(t, initialMetrics.CpuAllocated, newMetrics.CpuAllocated)
	assert.Equal(t, initialMetrics.MemoryAllocatedBytes, newMetrics.MemoryAllocatedBytes)
}
