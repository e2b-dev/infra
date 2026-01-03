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

func TestIsNodeCPUCompatible_FamilyMismatch(t *testing.T) {
	t.Parallel()
	// Same architecture but different family
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4, nodemanager.WithCPUInfo("x86_64", "AMD", "23"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "Intel", CPUModel: "6"}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.False(t, result, "Node should be incompatible when CPU family differs")
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

func TestIsNodeCPUCompatible_ModelMismatch(t *testing.T) {
	t.Parallel()
	// Same architecture and family but different CPU model
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4, nodemanager.WithCPUInfo("x86_64", "Intel", "6"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "Intel", CPUModel: "7"}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.False(t, result, "Node should be incompatible when CPU model differs")
}

func TestIsNodeCPUCompatible_ModelMatch_DifferentGenerations(t *testing.T) {
	t.Parallel()
	// Test that different Intel generations (model numbers) are incompatible
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4, nodemanager.WithCPUInfo("x86_64", "Intel", "85")) // Skylake
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "Intel", CPUModel: "143"}                   // Alder Lake

	result := isNodeCPUCompatible(node, buildCPU)
	assert.False(t, result, "Node should be incompatible when CPU models represent different processor generations")
}

func TestIsNodeCPUCompatible_AllFieldsMatch(t *testing.T) {
	t.Parallel()
	// Complete match including architecture, family, and model
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4, nodemanager.WithCPUInfo("x86_64", "Intel", "85"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "Intel", CPUModel: "85"}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.True(t, result, "Node should be compatible when architecture, family, and model all match")
}
