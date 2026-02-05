package proxy

import "context"

type PausedSandboxResumer interface {
	Resume(ctx context.Context, sandboxId string, timeoutSeconds int32) error
}
