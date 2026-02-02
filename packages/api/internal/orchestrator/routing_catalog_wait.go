package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

const routingCatalogWaitInterval = 200 * time.Millisecond

func (o *Orchestrator) WaitForSandboxInRoutingCatalog(ctx context.Context, sandboxID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for sandbox %s in routing catalog", sandboxID)
		}

		_, err := o.routingCatalog.GetSandbox(ctx, sandboxID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, e2bcatalog.ErrSandboxNotFound) {
			return err
		}

		time.Sleep(routingCatalogWaitInterval)
	}
}
