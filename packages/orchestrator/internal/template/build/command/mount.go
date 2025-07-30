package command

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

// Mount implements mounting templates using NBD to paths accessible by the parent OS
type Mount struct {
	TemplateStorage storage.StorageProvider
	DevicePool      *nbd.DevicePool
}

func (m *Mount) Execute(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata sandboxtools.CommandMetadata,
) (sandboxtools.CommandMetadata, error) {
	args := step.Args
	// args: [template_id build_id mount_path]
	if len(args) < 3 {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("MOUNT requires template_id, build_id, and mount_path arguments")
	}

	templateID := args[0]
	buildID := args[1]
	mountPath := args[2]

	postProcessor.Info(fmt.Sprintf("Mounting template %s/%s to %s", templateID, buildID, mountPath))

	// Validate mount path
	if !filepath.IsAbs(mountPath) {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("mount path must be absolute: %s", mountPath)
	}

	// Create mount directory if it doesn't exist
	if err := os.MkdirAll(mountPath, 0755); err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to create mount directory %s: %w", mountPath, err)
	}

	// Create template files metadata
	templateFiles := storage.TemplateFiles{
		TemplateID: templateID,
		BuildID:    buildID,
	}

	// Mount the template rootfs
	devicePath, err := m.mountTemplateRootfs(ctx, templateFiles, mountPath)
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to mount template rootfs: %w", err)
	}

	postProcessor.Info(fmt.Sprintf("Successfully mounted template to %s using device %s", mountPath, devicePath))

	return cmdMetadata, nil
}

// mountTemplateRootfs mounts a template's rootfs using NBD to the specified path
func (m *Mount) mountTemplateRootfs(ctx context.Context, templateFiles storage.TemplateFiles, mountPath string) (string, error) {
	// Get the template rootfs from storage
	rootfsObject, err := m.TemplateStorage.OpenObject(ctx, templateFiles.StorageRootfsPath())
	if err != nil {
		return "", fmt.Errorf("failed to open template rootfs from storage: %w", err)
	}

	// Create a temporary file to store the rootfs locally
	tempRootfsPath := filepath.Join("/tmp", fmt.Sprintf("template-%s-%s.ext4", templateFiles.TemplateID, templateFiles.BuildID))
	defer func() {
		// Clean up temp file on return
		if cleanupErr := os.Remove(tempRootfsPath); cleanupErr != nil {
			zap.L().Warn("Failed to clean up temporary rootfs file", zap.String("path", tempRootfsPath), zap.Error(cleanupErr))
		}
	}()

	// Download the rootfs to the temporary file
	tempFile, err := os.Create(tempRootfsPath)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer tempFile.Close()

	if _, err := rootfsObject.WriteTo(tempFile); err != nil {
		return "", fmt.Errorf("failed to download rootfs to temporary file: %w", err)
	}

	// Get an NBD device from the pool
	deviceSlot, err := m.DevicePool.GetDevice(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get NBD device from pool: %w", err)
	}

	devicePath := nbd.GetDevicePath(deviceSlot)

	// Setup NBD connection to the rootfs file
	if err := m.setupNBDDevice(tempRootfsPath, devicePath); err != nil {
		// Release the device back to the pool on error
		if releaseErr := m.DevicePool.ReleaseDevice(deviceSlot); releaseErr != nil {
			zap.L().Error("Failed to release NBD device on error", zap.Uint32("device_slot", deviceSlot), zap.Error(releaseErr))
		}
		return "", fmt.Errorf("failed to setup NBD device: %w", err)
	}

	// Mount the NBD device to the specified path
	if err := m.mountDevice(devicePath, mountPath); err != nil {
		// Disconnect NBD and release device on mount error
		if disconnectErr := m.disconnectNBDDevice(devicePath); disconnectErr != nil {
			zap.L().Error("Failed to disconnect NBD device on mount error", zap.String("device_path", devicePath), zap.Error(disconnectErr))
		}
		if releaseErr := m.DevicePool.ReleaseDevice(deviceSlot); releaseErr != nil {
			zap.L().Error("Failed to release NBD device on mount error", zap.Uint32("device_slot", deviceSlot), zap.Error(releaseErr))
		}
		return "", fmt.Errorf("failed to mount device to %s: %w", mountPath, err)
	}

	return devicePath, nil
}

// setupNBDDevice connects a file to an NBD device using qemu-nbd
func (m *Mount) setupNBDDevice(filePath, devicePath string) error {
	// Use qemu-nbd to connect the file to the NBD device
	cmd := exec.Command("qemu-nbd", "--connect="+devicePath, "--format=raw", filePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to setup NBD device %s for file %s: %w, output: %s", devicePath, filePath, err, string(output))
	}
	return nil
}

// mountDevice mounts a block device to a directory
func (m *Mount) mountDevice(devicePath, mountPath string) error {
	// Try to mount as ext4 first, which is the expected filesystem type for templates
	if err := syscall.Mount(devicePath, mountPath, "ext4", 0, ""); err != nil {
		// If ext4 mount fails, try with mount command for better error handling
		cmd := exec.Command("mount", "-t", "ext4", devicePath, mountPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to mount device %s to %s: %w, output: %s", devicePath, mountPath, err, string(output))
		}
	}
	return nil
}

// disconnectNBDDevice disconnects an NBD device
func (m *Mount) disconnectNBDDevice(devicePath string) error {
	cmd := exec.Command("qemu-nbd", "--disconnect", devicePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to disconnect NBD device %s: %w, output: %s", devicePath, err, string(output))
	}
	return nil
}