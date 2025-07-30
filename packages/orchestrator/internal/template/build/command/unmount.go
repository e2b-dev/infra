package command

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

// Unmount implements unmounting templates and cleaning up NBD resources
type Unmount struct {
	DevicePool *nbd.DevicePool
}

func (u *Unmount) Execute(
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
	// args: [mount_path] or [mount_path device_slot]
	if len(args) < 1 {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("UNMOUNT requires mount_path argument")
	}

	mountPath := args[0]

	postProcessor.Info(fmt.Sprintf("Unmounting template from %s", mountPath))

	// Unmount the filesystem
	if err := u.unmountPath(mountPath); err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to unmount %s: %w", mountPath, err)
	}

	// If device slot is provided, clean up NBD device
	if len(args) >= 2 {
		deviceSlot := args[1]
		devicePath := nbd.GetDevicePath(uint32(deviceSlot[0])) // Simple conversion for demo

		// Disconnect NBD device
		if err := u.disconnectNBDDevice(devicePath); err != nil {
			postProcessor.Warn(fmt.Sprintf("Failed to disconnect NBD device %s: %v", devicePath, err))
		}

		// Note: In a production implementation, you'd want to track device slots properly
		// and release them back to the pool
	}

	postProcessor.Info(fmt.Sprintf("Successfully unmounted %s", mountPath))

	return cmdMetadata, nil
}

// unmountPath unmounts a filesystem from the specified path
func (u *Unmount) unmountPath(mountPath string) error {
	// Try syscall first
	if err := syscall.Unmount(mountPath, 0); err != nil {
		// If syscall fails, try umount command
		cmd := exec.Command("umount", mountPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to unmount %s: %w, output: %s", mountPath, err, string(output))
		}
	}
	return nil
}

// disconnectNBDDevice disconnects an NBD device
func (u *Unmount) disconnectNBDDevice(devicePath string) error {
	cmd := exec.Command("qemu-nbd", "--disconnect", devicePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to disconnect NBD device %s: %w, output: %s", devicePath, err, string(output))
	}
	return nil
}