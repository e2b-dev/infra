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

// TODO: We should be able to parallelize the memfile copying with the FC start
var hugefileCache = NewHugefileCache()

type SandboxFiles struct {
	EnvPath      string
	BuildDirPath string

	EnvInstancePath string
	SocketPath      string

	KernelDirPath      string
	KernelMountDirPath string

	FirecrackerBinaryPath string

	MemfilePath string
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
	sandboxID,
	envID,
	kernelVersion,
	kernelsDir,
	kernelMountDir,
	kernelName,
	firecrackerBinaryPath string,
	hugePages bool,
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

	// Mount overlay
	buildIDPath := filepath.Join(envPath, BuildIDName)

	data, err := os.ReadFile(buildIDPath)
	if err != nil {
		return nil, fmt.Errorf("failed reading build id for the env %s: %w", envID, err)
	}

	buildID := string(data)
	buildDirPath := filepath.Join(envPath, BuildDirName, buildID)

	// Assemble socket path
	socketPath, sockErr := getSocketPath(sandboxID)
	if sockErr != nil {
		errMsg := fmt.Errorf("error getting socket path: %w", sockErr)
		telemetry.ReportCriticalError(childCtx, errMsg)
		return nil, errMsg
	}

	// Create kernel path
	kernelPath := filepath.Join(kernelsDir, kernelVersion)

	memfilePath := filepath.Join(envPath, MemfileName)

	if hugePages {
		// Create hugepages backed memfile
		hugefilePath, hugefileErr := hugefileCache.GetHugefilePath(memfilePath)
		if hugefileErr != nil {
			return nil, fmt.Errorf("failed to get hugefile: %w", hugefileErr)
		}

		memfilePath = hugefilePath

		telemetry.ReportEvent(childCtx, "hugefile cached")
	}

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
		MemfilePath:           memfilePath,
	}, nil
}

func (env *SandboxFiles) Ensure(ctx context.Context) error {
	err := os.MkdirAll(env.EnvInstancePath, 0o777)
	if err != nil {
		telemetry.ReportError(ctx, err)
	}

	mkdirErr := os.MkdirAll(env.BuildDirPath, 0o777)
	if mkdirErr != nil {
		telemetry.ReportError(ctx, err)
	}

	err = reflink.Always(
		filepath.Join(env.EnvPath, RootfsName),
		filepath.Join(env.EnvInstancePath, RootfsName),
	)
	if err != nil {
		errMsg := fmt.Errorf("error creating reflinked rootfs: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return errMsg
	}

	return nil
}

func (env *SandboxFiles) Cleanup(
	ctx context.Context,
	tracer trace.Tracer,
) error {
	childCtx, childSpan := tracer.Start(ctx, "cleanup-env-instance",
		trace.WithAttributes(
			attribute.String("instance.env_instance_path", env.EnvInstancePath),
			attribute.String("instance.build_dir_path", env.BuildDirPath),
			attribute.String("instance.env_path", env.EnvPath),
		),
	)
	defer childSpan.End()

	err := os.RemoveAll(env.EnvInstancePath)
	if err != nil {
		errMsg := fmt.Errorf("error deleting env instance files: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		// TODO: Check the socket?
		telemetry.ReportEvent(childCtx, "removed all env instance files")
	}

	// Remove socket
	err = os.Remove(env.SocketPath)
	if err != nil {
		errMsg := fmt.Errorf("error deleting socket: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "removed socket")
	}

	return nil
}
