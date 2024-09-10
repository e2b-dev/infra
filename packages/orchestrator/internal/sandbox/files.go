package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/KarpelesLab/reflink"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/template"
)

const (
	envsDisk = "/mnt/disks/fc-envs/v1"

	BuildDirName        = "builds"
	EnvInstancesDirName = "env-instances"

	socketWaitTimeout = 2 * time.Second
)

type SandboxFiles struct {
	UFFDSocketPath string

	EnvPath      string
	BuildDirPath string

	EnvInstancePath string
	SocketPath      string

	KernelDirPath      string
	KernelMountDirPath string

	FirecrackerBinaryPath string
}

// waitForSocket waits for the given file to exist.
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
	sandboxID,
	envID,
	buildID,
	kernelVersion,
	kernelsDir,
	kernelMountDir,
	kernelName,
	firecrackerBinaryPath string,
) (*SandboxFiles, error) {
	childCtx, childSpan := tracer.Start(ctx, "create-env-instance",
		trace.WithAttributes(
			attribute.String("env.id", envID),
			attribute.String("envs_disk", envsDisk),
		),
	)
	defer childSpan.End()

	envPath := filepath.Join(envsDisk, envID)
	envInstancePath := filepath.Join(envPath, EnvInstancesDirName, sandboxID)

	buildDirPath := filepath.Join(envPath, BuildDirName, buildID)

	// Assemble socket path
	socketPath, sockErr := getSocketPath(sandboxID)
	if sockErr != nil {
		errMsg := fmt.Errorf("error getting socket path: %w", sockErr)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	// Assemble UFFD socket path
	socketName := fmt.Sprintf("uffd-%s", sandboxID)

	uffdSocketPath, sockPathErr := getSocketPath(socketName)
	if sockPathErr != nil {
		errMsg := fmt.Errorf("error getting UFFD socket path: %w", sockPathErr)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	// Create kernel path
	kernelPath := filepath.Join(kernelsDir, kernelVersion)

	childSpan.SetAttributes(
		attribute.String("instance.env_instance_path", envInstancePath),
		attribute.String("instance.build.dir_path", buildDirPath),
		attribute.String("instance.env_path", envPath),
		attribute.String("instance.kernel.mount_path", filepath.Join(kernelMountDir, kernelName)),
		attribute.String("instance.kernel.path", filepath.Join(kernelPath, kernelName)),
		attribute.String("instance.firecracker.path", firecrackerBinaryPath),
	)

	return &SandboxFiles{
		EnvInstancePath:       envInstancePath,
		BuildDirPath:          buildDirPath,
		EnvPath:               envPath,
		SocketPath:            socketPath,
		KernelDirPath:         kernelPath,
		KernelMountDirPath:    kernelMountDir,
		FirecrackerBinaryPath: firecrackerBinaryPath,
		UFFDSocketPath:        uffdSocketPath,
	}, nil
}

func (f *SandboxFiles) Ensure(ctx context.Context) error {
	err := os.MkdirAll(f.EnvInstancePath, 0o777)
	if err != nil {
		telemetry.ReportError(ctx, err)
	}

	mkdirErr := os.MkdirAll(f.BuildDirPath, 0o777)
	if mkdirErr != nil {
		telemetry.ReportError(ctx, err)
	}

	err = reflink.Always(
		filepath.Join(f.EnvPath, template.RootfsName),
		filepath.Join(f.EnvInstancePath, template.RootfsName),
	)
	if err != nil {
		errMsg := fmt.Errorf("error creating reflinked rootfs: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return errMsg
	}

	return nil
}

func (f *SandboxFiles) Cleanup(
	ctx context.Context,
	tracer trace.Tracer,
) error {
	childCtx, childSpan := tracer.Start(ctx, "cleanup-env-instance",
		trace.WithAttributes(
			attribute.String("instance.env_instance_path", f.EnvInstancePath),
			attribute.String("instance.build_dir_path", f.BuildDirPath),
			attribute.String("instance.env_path", f.EnvPath),
		),
	)
	defer childSpan.End()

	err := os.RemoveAll(f.EnvInstancePath)
	if err != nil {
		errMsg := fmt.Errorf("error deleting env instance files: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		// TODO: Check the socket?
		telemetry.ReportEvent(childCtx, "removed all env instance files")
	}

	// Remove socket
	err = os.Remove(f.SocketPath)
	if err != nil {
		errMsg := fmt.Errorf("error deleting socket: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "removed socket")
	}

	// Remove UFFD socket
	err = os.Remove(f.UFFDSocketPath)
	if err != nil {
		errMsg := fmt.Errorf("error deleting socket for UFFD: %w", err)
		telemetry.ReportError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "removed UFFD socket")
	}

	return nil
}
