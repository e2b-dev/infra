package placement

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

// Tests for isNodeCPUCompatible

func TestIsNodeCPUCompatible_NoBuildRequirements(t *testing.T) {
	t.Parallel()
	// When build has no CPU requirements, all nodes should be compatible
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4, nodemanager.WithCPUInfo("x86_64", "Intel", "6"))
	buildCPU := machineinfo.MachineInfo{} // Empty - no requirements

	result := isNodeCPUCompatible(node, buildCPU)
	assert.True(t, result, "Node should be compatible when build has no CPU requirements")
}

func TestIsNodeCPUCompatible_ExactMatch(t *testing.T) {
	t.Parallel()
	// Node and build have matching CPU info
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4, nodemanager.WithCPUInfo("x86_64", "Intel", "6"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "Intel", CPUModel: "6"}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.True(t, result, "Node should be compatible when CPU info matches exactly")
}

func TestIsNodeCPUCompatible_ArchitectureMismatch(t *testing.T) {
	t.Parallel()
	// Different CPU architectures
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4, nodemanager.WithCPUInfo("aarch64", "ARM", "8"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "Intel", CPUModel: "6"}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.False(t, result, "Node should be incompatible with different architecture")
}

func TestIsNodeCPUCompatible_UnlistedModelMismatch(t *testing.T) {
	t.Parallel()
	// A build whose model is not paired with the node's model in the compatible
	// model map: incompatible.
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4,
		nodemanager.WithCPUInfo("x86_64", "6", "79"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: "85"}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.False(t, result, "Node with an unlisted different model should be incompatible")
}

func TestIsNodeCPUCompatible_NodeHasNoCPUInfo(t *testing.T) {
	t.Parallel()
	// Node without CPU info, build requires specific CPU
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4) // No CPU info
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "Intel", CPUModel: "6"}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.False(t, result, "Node without CPU info should be incompatible when build requires specific CPU")
}

func TestIsNodeCPUCompatible_BothEmpty(t *testing.T) {
	t.Parallel()
	// Both node and build have no CPU info
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4) // No CPU info
	buildCPU := machineinfo.MachineInfo{}                               // No requirements

	result := isNodeCPUCompatible(node, buildCPU)
	assert.True(t, result, "Node should be compatible when neither has CPU requirements")
}

func TestIsNodeCPUCompatible_OlderBuildOnNewerNode_Compatible(t *testing.T) {
	t.Parallel()
	// An Ice Lake build restored on a newer Emerald Rapids node: allowed because
	// the newer CPU is a superset of the older one.
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4,
		nodemanager.WithCPUInfo("x86_64", "6", machineinfo.EmeraldRapidsModel))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: machineinfo.IceLakeModel}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.True(t, result, "An older build should be allowed to run on a newer node")
}

func TestIsNodeCPUCompatible_NewerBuildOnOlderNode_Incompatible(t *testing.T) {
	t.Parallel()
	// An Emerald Rapids build restored on an older Ice Lake node: rejected because
	// the older CPU may lack instructions the newer build relies on. Compatibility
	// is directional (older build -> newer node only).
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4,
		nodemanager.WithCPUInfo("x86_64", "6", machineinfo.IceLakeModel))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: machineinfo.EmeraldRapidsModel}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.False(t, result, "A newer build should not be allowed to run on an older node")
}

func TestIsNodeCPUCompatible_FamilyMismatch(t *testing.T) {
	t.Parallel()
	// Same model number but different CPU family (e.g. Intel vs AMD): incompatible.
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4,
		nodemanager.WithCPUInfo("x86_64", "23", "85"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: "85"}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.False(t, result, "Same model with a different family should be incompatible")
}

func TestIsNodeCPUCompatible_AllFieldsMatch(t *testing.T) {
	t.Parallel()
	// Complete match including architecture, family, and model
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4, nodemanager.WithCPUInfo("x86_64", "6", "85"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: "85"}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.True(t, result, "Node should be compatible when architecture, family, and model all match")
}
