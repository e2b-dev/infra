package proxy

import "context"

type SandboxLifecycleClient interface {
	Init(ctx context.Context)

	// Resume attempts to resume/start the sandbox and returns a routable orchestrator IP on success.
	Resume(ctx context.Context, sandboxId string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) (string, error)

	// KeepAlive extends a running sandbox timeout after valid proxy traffic.
	KeepAlive(ctx context.Context, sandboxId string, teamID string, trafficAccessToken string) error
}
