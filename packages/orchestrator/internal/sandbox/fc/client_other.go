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

func (c *apiClient) loadSnapshot(ctx context.Context, uffdSocketPath string, uffdReady chan struct{}, snapfile template.File) error {
	return nil
}

func (c *apiClient) resumeVM(ctx context.Context) error {
	return nil
}

func (c *apiClient) pauseVM(ctx context.Context) error {
	return nil
}

func (c *apiClient) createSnapshot(ctx context.Context, snapfilePath string, memfilePath string) error {
	return nil
}

func (c *apiClient) setMmds(ctx context.Context, metadata *MmdsMetadata) error {
	return nil
}

func (c *apiClient) setBootSource(ctx context.Context, kernelArgs string, kernelPath string) error {
	return nil
}

func (c *apiClient) setRootfsDrive(ctx context.Context, rootfsDrivePath string) error {
	return nil
}

func (c *apiClient) setNetworkInterface(ctx context.Context, vpeerName string, tapName string, tapMAC string) error {
	return nil
}

func (c *apiClient) setMachineConfig(ctx context.Context, vCPUCount int64, memoryMB int64, hugePages bool) error {
	return nil
}

func (c *apiClient) setEntropyDevice(ctx context.Context, size int64, oneTimeBurst int64, refillTime int64) error {
	return nil
}

func (c *apiClient) startVM(ctx context.Context) error {
	return nil
}
