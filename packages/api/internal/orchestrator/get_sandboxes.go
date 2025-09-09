package orchestrator

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/store"
)

func (o *Orchestrator) GetSandbox(ctx context.Context, sandboxID string, includeEvicting bool) (*store.Sandbox, error) {
	item, err := o.sandboxStore.Get(ctx, sandboxID, includeEvicting)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox '%s': %w", sandboxID, err)
	}

	return item, nil
}
