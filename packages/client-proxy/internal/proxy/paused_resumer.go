package proxy

import "context"

type PausedSandboxResumer interface {
	Init(ctx context.Context)

	// Resume attempts to resume/start the sandbox and returns a routable orchestrator IP on success.
	Resume(ctx context.Context, sandboxId string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) (string, error)
}
