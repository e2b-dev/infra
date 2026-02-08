package fc

import (
	"context"
	"fmt"

	"github.com/bits-and-blooms/bitset"
	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/go-openapi/strfmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client/operations"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
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
	ctx, span := tracer.Start(ctx, "load-snapshot")
	defer span.End()

	backendType := models.MemoryBackendBackendTypeUffd
	backend := &models.MemoryBackend{
		BackendPath: &uffdSocketPath,
		BackendType: &backendType,
	}

	snapfilePath := snapfile.Path()

	telemetry.ReportEvent(ctx, "got snapfile path")

	snapshotConfig := operations.LoadSnapshotParams{
		Context: ctx,
		Body: &models.SnapshotLoadParams{
			ResumeVM:            false,
			EnableDiffSnapshots: false,
			MemBackend:          backend,
			SnapshotPath:        &snapfilePath,
		},
	}

	_, err := c.client.Operations.LoadSnapshot(&snapshotConfig)
	if err != nil {
		return fmt.Errorf("error loading snapshot: %w", err)
	}

	telemetry.ReportEvent(ctx, "loaded snapshot")

	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled while waiting for uffd ready: %w", ctx.Err())
	case <-uffdReady:
		// Wait for the uffd to be ready to serve requests
	}

	telemetry.ReportEvent(ctx, "uffd ready")

	return nil
}

func (c *apiClient) resumeVM(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "resume-vm")
	defer span.End()

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
) error {
	snapshotConfig := operations.CreateSnapshotParams{
		Context: ctx,
		Body: &models.SnapshotCreateParams{
			SnapshotType: models.SnapshotCreateParamsSnapshotTypeFull,
			SnapshotPath: &snapfilePath,
		},
	}

	_, err := c.client.Operations.CreateSnapshot(&snapshotConfig)
	if err != nil {
		return fmt.Errorf("error creating snapshot: %w", err)
	}

	return nil
}

func (c *apiClient) setMmds(ctx context.Context, metadata *MmdsMetadata) error {
	ctx, span := tracer.Start(ctx, "set-mmds")
	defer span.End()

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

func (c *apiClient) setBootSource(ctx context.Context, kernelArgs string, kernelPath string) error {
	bootSourceConfig := operations.PutGuestBootSourceParams{
		Context: ctx,
		Body: &models.BootSource{
			BootArgs:        kernelArgs,
			KernelImagePath: &kernelPath,
		},
	}

	_, err := c.client.Operations.PutGuestBootSource(&bootSourceConfig)

	return err
}

func (c *apiClient) setRootfsDrive(ctx context.Context, rootfsPath string, ioEngine *string) error {
	rootfs := "rootfs"

	isRootDevice := true
	driversConfig := operations.PutGuestDriveByIDParams{
		Context: ctx,
		DriveID: rootfs,
		Body: &models.Drive{
			DriveID:      &rootfs,
			PathOnHost:   rootfsPath,
			IsRootDevice: &isRootDevice,
			IsReadOnly:   false,
			IoEngine:     ioEngine,
		},
	}

	_, err := c.client.Operations.PutGuestDriveByID(&driversConfig)
	if err != nil {
		return fmt.Errorf("error setting fc drivers config: %w", err)
	}

	return nil
}

func (c *apiClient) setNetworkInterface(ctx context.Context, ifaceID string, tapName string, tapMac string) error {
	networkConfig := operations.PutGuestNetworkInterfaceByIDParams{
		Context: ctx,
		IfaceID: ifaceID,
		Body: &models.NetworkInterface{
			IfaceID:     &ifaceID,
			GuestMac:    tapMac,
			HostDevName: &tapName,
		},
	}

	_, err := c.client.Operations.PutGuestNetworkInterfaceByID(&networkConfig)
	if err != nil {
		return fmt.Errorf("error setting fc network config: %w", err)
	}

	mmdsVersion := "V2"
	mmdsConfig := operations.PutMmdsConfigParams{
		Context: ctx,
		Body: &models.MmdsConfig{
			Version:           &mmdsVersion,
			NetworkInterfaces: []string{ifaceID},
		},
	}

	_, err = c.client.Operations.PutMmdsConfig(&mmdsConfig)
	if err != nil {
		return fmt.Errorf("error setting network mmds data: %w", err)
	}

	return nil
}

func (c *apiClient) setMachineConfig(
	ctx context.Context,
	vCPUCount int64,
	memoryMB int64,
	hugePages bool,
) error {
	smt := true
	trackDirtyPages := false
	machineConfig := &models.MachineConfiguration{
		VcpuCount:       &vCPUCount,
		MemSizeMib:      &memoryMB,
		Smt:             &smt,
		TrackDirtyPages: &trackDirtyPages,
	}
	if hugePages {
		machineConfig.HugePages = models.MachineConfigurationHugePagesNr2M
	}
	machineConfigParams := operations.PutMachineConfigurationParams{
		Context: ctx,
		Body:    machineConfig,
	}
	_, err := c.client.Operations.PutMachineConfiguration(&machineConfigParams)
	if err != nil {
		return fmt.Errorf("error setting fc machine config: %w", err)
	}

	return nil
}

// https://github.com/firecracker-microvm/firecracker/blob/main/docs/entropy.md#firecracker-implementation
func (c *apiClient) setEntropyDevice(ctx context.Context) error {
	entropyConfig := operations.PutEntropyDeviceParams{
		Context: ctx,
		Body: &models.EntropyDevice{
			RateLimiter: &models.RateLimiter{
				Bandwidth: &models.TokenBucket{
					OneTimeBurst: utils.ToPtr(entropyOneTimeBurst),
					Size:         utils.ToPtr(entropyBytesSize),
					RefillTime:   utils.ToPtr(entropyRefillTime),
				},
			},
		},
	}

	_, err := c.client.Operations.PutEntropyDevice(&entropyConfig)
	if err != nil {
		return fmt.Errorf("error setting fc entropy config: %w", err)
	}

	return nil
}

func (c *apiClient) startVM(ctx context.Context) error {
	start := models.InstanceActionInfoActionTypeInstanceStart
	startActionParams := operations.CreateSyncActionParams{
		Context: ctx,
		Info: &models.InstanceActionInfo{
			ActionType: &start,
		},
	}

	_, err := c.client.Operations.CreateSyncAction(&startActionParams)
	if err != nil {
		return fmt.Errorf("error starting fc: %w", err)
	}

	return nil
}

// enableFreePageReporting configures the balloon device with free page reporting.
// This must be called pre-boot (before starting the VM).
// Free page reporting allows the guest to continuously report unused memory ranges,
// which Firecracker will madvise with MADV_DONTNEED to reduce RSS.
func (c *apiClient) enableFreePageReporting(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "enable-free-page-reporting")
	defer span.End()

	amountMib := int64(0)
	deflateOnOom := false

	balloonConfig := operations.PutBalloonParams{
		Context: ctx,
		Body: &models.Balloon{
			AmountMib:         &amountMib,
			DeflateOnOom:      &deflateOnOom,
			FreePageReporting: true,
		},
	}

	_, err := c.client.Operations.PutBalloon(&balloonConfig)
	if err != nil {
		return fmt.Errorf("error setting balloon config: %w", err)
	}

	return nil
}

func (c *apiClient) memoryMapping(ctx context.Context) (*memory.Mapping, error) {
	params := operations.GetMemoryMappingsParams{
		Context: ctx,
	}

	res, err := c.client.Operations.GetMemoryMappings(&params)
	if err != nil {
		return nil, fmt.Errorf("error getting memory mappings: %w", err)
	}

	return memory.NewMappingFromFc(res.Payload.Mappings)
}

// Info about the initial memory of the sandbox.
// This should be only called after the first Firecracker start, before any resumes.
// If the sandbox is resumed, use the dirtyMemory instead.
func (c *apiClient) initialMemory(ctx context.Context, blockSize int64) (*header.DiffMetadata, error) {
	params := operations.GetMemoryParams{
		Context: ctx,
	}

	res, err := c.client.Operations.GetMemory(&params)
	if err != nil {
		return nil, fmt.Errorf("error getting memory: %w", err)
	}

	return &header.DiffMetadata{
		Dirty:     bitset.From(res.Payload.Resident),
		Empty:     bitset.From(res.Payload.Empty),
		BlockSize: blockSize,
	}, nil
}

// Info about the dirty memory of the sandbox.
func (c *apiClient) dirtyMemory(ctx context.Context, blockSize int64) (*header.DiffMetadata, error) {
	params := operations.GetDirtyMemoryParams{
		Context: ctx,
	}

	res, err := c.client.Operations.GetDirtyMemory(&params)
	if err != nil {
		return nil, fmt.Errorf("error getting memory: %w", err)
	}

	return &header.DiffMetadata{
		Dirty:     bitset.From(res.Payload.Bitmap),
		Empty:     bitset.New(0),
		BlockSize: blockSize,
	}, nil
}
