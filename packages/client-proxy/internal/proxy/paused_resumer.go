package proxy

import "context"

// SandboxResumer is the one call the resume flights share with the resumer
// they wrap.
type SandboxResumer interface {
	// Resume attempts to resume/start the sandbox and returns a routable orchestrator IP on success.
	Resume(ctx context.Context, sandboxId string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) (string, error)
}

// PausedSandboxResumer is the resumer main wires and initialises: Init
// observes the connection it holds. The proxy takes only SandboxResumer.
type PausedSandboxResumer interface {
	Init(ctx context.Context)
	SandboxResumer
}
