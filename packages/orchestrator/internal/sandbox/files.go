package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	BuildIDName  = "build_id"
	RootfsName   = "rootfs.ext4"
	SnapfileName = "snapfile"
	MemfileName  = "memfile"
	envsDisk     = "/mnt/disks/fc-envs/v1"

	BuildDirName        = "builds"
	EnvInstancesDirName = "env-instances"

	socketWaitTimeout = 2 * time.Second
)

type SandboxFiles struct {
	UFFDSocketPath string
	SocketPath     string

	KernelDirPath      string
	KernelMountDirPath string

	FirecrackerBinaryPath string
	UFFDBinaryPath        string
}

// waitForSocket waits for the given file to exist
func waitForSocket(socketPath string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	ticker := time.NewTicker(10 * time.Millisecond)

	defer func() {
		cancel()
		ticker.Stop()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(socketPath); err != nil {
				continue
			}

			// TODO: Send test HTTP request to make sure socket is available
			return nil
		}
	}
}

func newSandboxFiles(
	ctx context.Context,
	tracer trace.Tracer,
	slot *IPSlot,
	kernelVersion,
	kernelsDir,
	kernelMountDir,
	kernelName,
	firecrackerBinaryPath,
	uffdBinaryPath string,
) (*SandboxFiles, error) {
	childCtx, childSpan := tracer.Start(ctx, "create-env-instance",
		trace.WithAttributes(
			attribute.String("envs_disk", envsDisk),
		),
	)
	defer childSpan.End()

	// Assemble socket path
	socketPath, sockErr := getSocketPath(strconv.Itoa(slot.SlotIdx))
	if sockErr != nil {
		errMsg := fmt.Errorf("error getting socket path: %w", sockErr)
		telemetry.ReportCriticalError(childCtx, errMsg)
		return nil, errMsg
	}

	socketName := fmt.Sprintf("uffd-%d", slot.SlotIdx)
	socket, sockErr := getSocketPath(socketName)
	if sockErr != nil {
		errMsg := fmt.Errorf("error getting UFFD socket path: %w", sockErr)
		telemetry.ReportCriticalError(childCtx, errMsg)
		return nil, errMsg
	}

	// Create kernel path
	kernelPath := filepath.Join(kernelsDir, kernelVersion)

	childSpan.SetAttributes(
		attribute.String("instance.kernel.mount_path", filepath.Join(kernelMountDir, kernelName)),
		attribute.String("instance.kernel.path", filepath.Join(kernelPath, kernelName)),
		attribute.String("instance.firecracker.path", firecrackerBinaryPath),
	)

	return &SandboxFiles{
		SocketPath:            socketPath,
		KernelDirPath:         kernelPath,
		KernelMountDirPath:    kernelMountDir,
		FirecrackerBinaryPath: firecrackerBinaryPath,
		UFFDSocketPath:        socket,
		UFFDBinaryPath:        uffdBinaryPath,
	}, nil
}

//
//func (env *SandboxFiles) Ensure(ctx context.Context) error {
//	err := os.MkdirAll(env.EnvInstancePath, 0o777)
//	if err != nil {
//		telemetry.ReportError(ctx, err)
//	}
//
//	mkdirErr := os.MkdirAll(env.BuildDirPath, 0o777)
//	if mkdirErr != nil {
//		telemetry.ReportError(ctx, err)
//	}
//
//	err = reflink.Always(
//		filepath.Join(env.EnvPath, RootfsName),
//		filepath.Join(env.EnvInstancePath, RootfsName),
//	)
//	if err != nil {
//		errMsg := fmt.Errorf("error creating reflinked rootfs: %w", err)
//		telemetry.ReportCriticalError(ctx, errMsg)
//
//		return errMsg
//	}
//
//	return nil
//}

func (env *SandboxFiles) Cleanup(
	ctx context.Context,
	tracer trace.Tracer,
) error {
	childCtx, childSpan := tracer.Start(ctx, "cleanup-env-instance")
	defer childSpan.End()

	// Remove socket
	err := os.Remove(env.SocketPath)
	if err != nil {
		errMsg := fmt.Errorf("error deleting socket: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "removed socket")
	}

	// Remove UFFD socket
	err = os.Remove(env.UFFDSocketPath)
	if err != nil {
		errMsg := fmt.Errorf("error deleting socket for UFFD: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "removed UFFD socket")
	}

	return nil
}
