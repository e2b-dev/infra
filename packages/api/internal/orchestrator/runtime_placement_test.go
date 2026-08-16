package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func TestAddRuntimePlacementLabelsPinsExplicitKataRuntime(t *testing.T) {
	labels, enabled := addRuntimePlacementLabels(map[string]string{
		consts.OrchestratorRuntimeClassMetadataKey: "kata-qemu",
	}, []string{"team-label"}, false)

	assert.True(t, enabled)
	assert.Equal(t, []string{"team-label", consts.OrchestratorRuntimeOCIKataLabel, "kata-qemu"}, labels)
}

func TestAddRuntimePlacementLabelsLeavesDefaultPlacementUnchanged(t *testing.T) {
	labels, enabled := addRuntimePlacementLabels(nil, []string{"default"}, false)

	assert.False(t, enabled)
	assert.Equal(t, []string{"default"}, labels)
}
