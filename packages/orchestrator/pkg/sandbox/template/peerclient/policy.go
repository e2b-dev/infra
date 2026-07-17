package peerclient

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type sourcePolicyKey struct{}

func WithSourcePolicy(ctx context.Context, policy orchestrator.SnapshotSourcePolicy) context.Context {
	return context.WithValue(ctx, sourcePolicyKey{}, policy)
}

func sourcePolicyFromContext(ctx context.Context) orchestrator.SnapshotSourcePolicy {
	policy, ok := ctx.Value(sourcePolicyKey{}).(orchestrator.SnapshotSourcePolicy)
	if !ok {
		return orchestrator.SnapshotSourcePolicy_SNAPSHOT_SOURCE_POLICY_AUTO
	}

	return policy
}
