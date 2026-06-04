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

func TestIsNodeCPUCompatible_NodeMissingFlag(t *testing.T) {
	t.Parallel()
	// Node lacks a CPU flag the build requires (e.g. an n2 build using AVX-512
	// placed on an older n1 node without it): incompatible.
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4,
		nodemanager.WithCPUInfo("x86_64", "Intel", "79"),
		nodemanager.WithCPUFlags("sse2", "avx", "avx2"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFlags: []string{"sse2", "avx", "avx2", "avx512f"}}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.False(t, result, "Node missing a required CPU flag should be incompatible")
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

func TestIsNodeCPUCompatible_ModelDiffers_StillCompatible(t *testing.T) {
	t.Parallel()
	// Different CPU model but the node provides every flag the build needs:
	// compatible, because compatibility is decided by the instruction set.
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4,
		nodemanager.WithCPUInfo("x86_64", "Intel", "6"),
		nodemanager.WithCPUFlags("sse2", "avx", "avx2"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFlags: []string{"sse2", "avx"}}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.True(t, result, "Node should be compatible when it provides all required CPU flags")
}

func TestIsNodeCPUCompatible_DifferentGenerations_StillCompatible(t *testing.T) {
	t.Parallel()
	// n2 (Cascade Lake) build restored on an n4 (Emerald Rapids) node: the newer
	// generation's flags are a superset, so it's compatible.
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4,
		nodemanager.WithCPUInfo("x86_64", "Intel", "207"), // n4 Emerald Rapids
		nodemanager.WithCPUFlags("sse2", "avx", "avx2", "avx512f", "avx512_bf16"))
	buildCPU := machineinfo.MachineInfo{ // n2 Cascade Lake
		CPUArchitecture: "x86_64",
		CPUFlags:        []string{"sse2", "avx", "avx2", "avx512f"},
	}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.True(t, result, "An older-generation build should run on a newer node whose flags are a superset")
}

func TestIsNodeCPUCompatible_NoBuildFlags_FamilyMismatch(t *testing.T) {
	t.Parallel()
	// Older build with no recorded flags must fall back to family/model and be
	// rejected on a node of a different generation, not pass on arch alone.
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4,
		nodemanager.WithCPUInfo("x86_64", "Intel", "207"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "Intel", CPUModel: "85"}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.False(t, result, "Build without flags should fall back to family/model and reject a different model")
}

func TestIsNodeCPUCompatible_AllFieldsMatch(t *testing.T) {
	t.Parallel()
	// Complete match including architecture, family, and model
	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 2, 4, nodemanager.WithCPUInfo("x86_64", "Intel", "85"))
	buildCPU := machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "Intel", CPUModel: "85"}

	result := isNodeCPUCompatible(node, buildCPU)
	assert.True(t, result, "Node should be compatible when architecture, family, and model all match")
}
