package proxy

import "context"

type PausedSandboxResumer interface {
	// Resume attempts to resume/start the sandbox and returns a routable orchestrator IP on success.
	Resume(ctx context.Context, sandboxId string, timeoutSeconds int32) (string, error)
}
