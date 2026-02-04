package proxy

import "context"

type PausedSandboxChecker interface {
	Resume(ctx context.Context, sandboxId string, timeoutSeconds int32) error
}
