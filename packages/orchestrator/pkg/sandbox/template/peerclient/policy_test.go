package peerclient

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestSourcePolicyFromContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy *orchestrator.SnapshotSourcePolicy
		want   orchestrator.SnapshotSourcePolicy
	}{
		{
			name: "missing defaults to auto",
			want: orchestrator.SnapshotSourcePolicy_SNAPSHOT_SOURCE_POLICY_AUTO,
		},
		{
			name:   "stored policy",
			policy: new(orchestrator.SnapshotSourcePolicy_SNAPSHOT_SOURCE_POLICY_REQUIRE_PEER),
			want:   orchestrator.SnapshotSourcePolicy_SNAPSHOT_SOURCE_POLICY_REQUIRE_PEER,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tt.policy != nil {
				ctx = WithSourcePolicy(ctx, *tt.policy)
			}

			assert.Equal(t, tt.want, sourcePolicyFromContext(ctx))
		})
	}
}
