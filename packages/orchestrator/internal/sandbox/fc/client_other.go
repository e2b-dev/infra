//go:build !linux
// +build !linux

package fc

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
)

type apiClient struct {
}

func newApiClient(socketPath string) *apiClient {
	return nil
}

// internal/sandbox/fc/process.go:260:17: p.client.loadSnapshot undefined (type *apiClient has no field or method loadSnapshot)
// internal/sandbox/fc/process.go:272:17: p.client.resumeVM undefined (type *apiClient has no field or method resumeVM)
// internal/sandbox/fc/process.go:279:17: p.client.setMmds undefined (type *apiClient has no field or method setMmds)
// internal/sandbox/fc/process.go:320:18: p.client.pauseVM undefined (type *apiClient has no field or method pauseVM)
// internal/sandbox/fc/process.go:328:18: p.client.createSnapshot undefined (type *apiClient has no field or method createSnapshot)

func (c *apiClient) loadSnapshot(ctx context.Context, uffdSocketPath string, uffdReady chan struct{}, snapfile template.File) error {
	return nil
}

func (c *apiClient) resumeVM(ctx context.Context) error {
	return nil
}

func (c *apiClient) setMmds(ctx context.Context, metadata *MmdsMetadata) error {
	return nil
}

func (c *apiClient) pauseVM(ctx context.Context) error {
	return nil
}

func (c *apiClient) createSnapshot(ctx context.Context, snapfilePath string, memfilePath string) error {
	return nil
}
