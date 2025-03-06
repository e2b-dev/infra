//go:build linux
// +build linux

package fc

import (
	"context"
	"fmt"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/go-openapi/strfmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/socket"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client/operations"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"
)

type apiClient struct {
	client *client.Firecracker
}

func newApiClient(socketPath string) *apiClient {
	client := client.NewHTTPClient(strfmt.NewFormats())

	transport := firecracker.NewUnixSocketTransport(socketPath, nil, false)
	client.SetTransport(transport)

	return &apiClient{
		client: client,
	}
}

func (c *apiClient) loadSnapshot(
	ctx context.Context,
	uffdSocketPath string,
	uffdReady chan struct{},
	snapfile template.File,
) error {
	err := socket.Wait(ctx, uffdSocketPath)
	if err != nil {
		return fmt.Errorf("error waiting for uffd socket: %w", err)
	}

	backendType := models.MemoryBackendBackendTypeUffd
	backend := &models.MemoryBackend{
		BackendPath: &uffdSocketPath,
		BackendType: &backendType,
	}

	snapfilePath := snapfile.Path()
	snapshotConfig := operations.LoadSnapshotParams{
		Context: ctx,
		Body: &models.SnapshotLoadParams{
			ResumeVM:            false,
			EnableDiffSnapshots: false,
			MemBackend:          backend,
			SnapshotPath:        &snapfilePath,
		},
	}

	_, err = c.client.Operations.LoadSnapshot(&snapshotConfig)
	if err != nil {
		return fmt.Errorf("error loading snapshot: %w", err)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled while waiting for uffd ready: %w", ctx.Err())
	case <-uffdReady:
		// Wait for the uffd to be ready to serve requests
	}

	return nil
}

func (c *apiClient) resumeVM(ctx context.Context) error {
	state := models.VMStateResumed
	pauseConfig := operations.PatchVMParams{
		Context: ctx,
		Body: &models.VM{
			State: &state,
		},
	}

	_, err := c.client.Operations.PatchVM(&pauseConfig)
	if err != nil {
		return fmt.Errorf("error resuming vm: %w", err)
	}

	return nil
}

func (c *apiClient) pauseVM(ctx context.Context) error {
	state := models.VMStatePaused
	pauseConfig := operations.PatchVMParams{
		Context: ctx,
		Body: &models.VM{
			State: &state,
		},
	}

	_, err := c.client.Operations.PatchVM(&pauseConfig)
	if err != nil {
		return fmt.Errorf("error pausing vm: %w", err)
	}

	return nil
}

func (c *apiClient) createSnapshot(
	ctx context.Context,
	snapfilePath string,
	memfilePath string,
) error {
	snapshotConfig := operations.CreateSnapshotParams{
		Context: ctx,
		Body: &models.SnapshotCreateParams{
			MemFilePath:  &memfilePath,
			SnapshotPath: &snapfilePath,
		},
	}

	_, err := c.client.Operations.CreateSnapshot(&snapshotConfig)
	if err != nil {
		return fmt.Errorf("error loading snapshot: %w", err)
	}

	return nil
}

func (c *apiClient) setMmds(ctx context.Context, metadata *MmdsMetadata) error {
	mmdsConfig := operations.PutMmdsParams{
		Context: ctx,
		Body:    metadata,
	}

	_, err := c.client.Operations.PutMmds(&mmdsConfig)
	if err != nil {
		return fmt.Errorf("error setting mmds data: %w", err)
	}

	return nil
}
