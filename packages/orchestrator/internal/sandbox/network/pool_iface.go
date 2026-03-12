package network

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

// PoolInterface abstracts the network slot pool so v1 (*Pool) and v2 (V2Pool) can be swapped.
// The existing *Pool already satisfies this interface.
type PoolInterface interface {
	Get(ctx context.Context, network *orchestrator.SandboxNetworkConfig) (*Slot, error)
	Return(ctx context.Context, slot *Slot) error
	Close(ctx context.Context) error
}
