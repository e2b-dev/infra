package orchestrator

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func TestRouteNodeIPAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		node  *nodemanager.Node
		local bool
		want  string
	}{
		{
			name:  "uses node ip when present",
			node:  &nodemanager.Node{ClusterID: uuid.New(), IPAddress: "10.0.0.1"},
			local: false,
			want:  "10.0.0.1",
		},
		{
			name:  "local cluster falls back for local ci",
			node:  &nodemanager.Node{ClusterID: consts.LocalClusterID},
			local: true,
			want:  localSandboxIPAddress,
		},
		{
			name:  "local cluster stays empty outside local env",
			node:  &nodemanager.Node{ClusterID: consts.LocalClusterID},
			local: false,
			want:  "",
		},
		{
			name:  "remote cluster stays empty",
			node:  &nodemanager.Node{ClusterID: uuid.New()},
			local: true,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, routeNodeIPAddress(tt.node, tt.local))
		})
	}
}
