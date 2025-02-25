package orchestrator

import (
	"context"
	"fmt"
	"time"
)

func (o *Orchestrator) KeepAliveFor(ctx context.Context, sandboxID string, duration time.Duration, allowShorter bool) error {
	sbx, err := o.instanceCache.KeepAliveFor(sandboxID, duration, allowShorter)
	if err != nil {
		return fmt.Errorf("failed to keep alive for sandbox '%s': %w", sandboxID, err)
	}

	err = o.UpdateSandbox(ctx, sbx.Instance.SandboxID, sbx.EndTime, sbx.Instance.ClientID)
	return err
}
