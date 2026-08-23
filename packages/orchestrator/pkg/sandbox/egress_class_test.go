//go:build linux

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

// The whole per-class DSCP split hangs off this mapping: if a build sandbox
// stops reporting EgressClassBuild, build egress silently reverts to the
// sandbox class and nothing else in the stack notices.
func TestSandboxType_EgressClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sandboxType SandboxType
		want        network.EgressClass
	}{
		{name: "build sandboxes get the build class", sandboxType: SandboxTypeBuild, want: network.EgressClassBuild},
		{name: "regular sandboxes get the sandbox class", sandboxType: SandboxTypeSandbox, want: network.EgressClassSandbox},
		{name: "the empty type is a regular sandbox", sandboxType: "", want: network.EgressClassSandbox},
		{name: "an unknown type falls back to the sandbox class", sandboxType: SandboxType("something-else"), want: network.EgressClassSandbox},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tt.sandboxType.EgressClass())
		})
	}
}
