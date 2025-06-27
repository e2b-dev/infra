//go:build !linux
// +build !linux

package fc

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
)

type apiClient struct{}

func newApiClient(socketPath string) *apiClient {
	return nil
}

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
