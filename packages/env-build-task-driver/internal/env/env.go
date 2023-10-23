package env

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	_ "embed"

	"go.opentelemetry.io/otel/trace"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"

	"github.com/e2b-dev/infra/packages/env-build-task-driver/internal/telemetry"
)

const (
	buildIDName  = "build_id"
	rootfsName   = "rootfs.ext4"
	snapfileName = "snapfile"
	memfileName  = "memfile"

	buildDirName = "builds"
)

type Env struct {
	// Unique ID of the env.
	EnvID string
	// Unique ID of the build - this is used to distinguish builds of the same env that can start simultaneously.
	BuildID string

	// Path to the directory where all envs are stored.
	EnvsDiskPath string

	// Path to the directory where all docker contexts are stored. This directory is a FUSE mounted bucket where the contexts were uploaded.
	DockerContextsPath string

	// Docker registry where the docker images are uploaded for archivation/caching.
	DockerRegistry string

	// Path to where the kernel image is stored.
	KernelImagePath string

	// Path to the firecracker binary.
	FirecrackerBinaryPath string

	// Path to the envd.
	EnvdPath string

	// Path to the pkgs to install.
	// Only installs packages in this dir not packages in subdirs.
	PkgsPath string

	ContextFileName string

	// The number of vCPUs to allocate to the VM.
	VCpuCount int64

	// The amount of RAM memory to allocate to the VM, in MiB.
	MemoryMB int64

	// The amount of free disk to allocate to the VM, in MiB.
	DiskSizeMB int64

	// Path to the directory where the temporary files for the build are stored.
	BuildLogsWriter io.Writer

	// Google service account JSON secret base64 encoded.
	GoogleServiceAccountBase64 string
}

//go:embed provision.sh
var provisionEnvScriptFile string

// Provision script to run to set necessary things in the env.
func (e *Env) ProvisionScript() string {
	return provisionEnvScriptFile
}

// Path to the docker context.
func (e *Env) DockerContextPath() string {
	return filepath.Join(e.DockerContextsPath, e.EnvID, e.BuildID, e.ContextFileName)
}

// Path to the directory where the temporary files for the build are stored.
func (e *Env) tmpBuildDirPath() string {
	return filepath.Join(e.envDirPath(), buildDirName, e.BuildID)
}

// Path to the file where the build ID is stored. This is used for setting up the namespaces when starting the FC snapshot for this build/env.
func (e *Env) tmpBuildIDFilePath() string {
	return filepath.Join(e.tmpBuildDirPath(), buildIDName)
}

func (e *Env) tmpRootfsPath() string {
	return filepath.Join(e.tmpBuildDirPath(), rootfsName)
}

func (e *Env) tmpMemfilePath() string {
	return filepath.Join(e.tmpBuildDirPath(), memfileName)
}

func (e *Env) tmpSnapfilePath() string {
	return filepath.Join(e.tmpBuildDirPath(), snapfileName)
}

// Path to the directory where the env is stored.
func (e *Env) envDirPath() string {
	return filepath.Join(e.EnvsDiskPath, e.EnvID)
}

func (e *Env) envBuildIDFilePath() string {
	return filepath.Join(e.envDirPath(), buildIDName)
}

func (e *Env) envRootfsPath() string {
	return filepath.Join(e.envDirPath(), rootfsName)
}

func (e *Env) envMemfilePath() string {
	return filepath.Join(e.envDirPath(), memfileName)
}

func (e *Env) envSnapfilePath() string {
	return filepath.Join(e.envDirPath(), snapfileName)
}

func (e *Env) Build(ctx context.Context, tracer trace.Tracer, docker *client.Client, legacyDocker *docker.Client) error {
	childCtx, childSpan := tracer.Start(ctx, "build")
	defer childSpan.End()

	err := e.initialize(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("error initializing directories for building env '%s' during build '%s': %w", e.EnvID, e.BuildID, err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	defer e.Cleanup(childCtx, tracer)

	rootfs, err := NewRootfs(childCtx, tracer, e, docker, legacyDocker)
	if err != nil {
		errMsg := fmt.Errorf("error creating rootfs for env '%s' during build '%s': %w", e.EnvID, e.BuildID, err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	defer func() {
		go func() {
			pushContext, pushSpan := tracer.Start(
				trace.ContextWithSpanContext(context.Background(), childSpan.SpanContext()),
				"push-docker-image-and-cleanup",
			)
			defer pushSpan.End()
			defer rootfs.cleanupDockerImage(pushContext, tracer)

			if err != nil {
				// We will not push the docker image if we failed to create the rootfs.
				return
			}

			err := rootfs.pushDockerImage(pushContext, tracer)
			if err != nil {
				errMsg := fmt.Errorf("error pushing docker image %w", err)
				telemetry.ReportCriticalError(pushContext, errMsg)
			} else {
				telemetry.ReportEvent(pushContext, "pushed docker image")
			}
		}()
	}()

	network, err := NewFCNetwork(childCtx, tracer, e)
	if err != nil {
		errMsg := fmt.Errorf("error network setup for FC while building env '%s' during build '%s': %w", e.EnvID, e.BuildID, err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	defer network.Cleanup(childCtx, tracer)

	_, err = NewSnapshot(childCtx, tracer, e, network, rootfs)
	if err != nil {
		errMsg := fmt.Errorf("error snapshot for env '%s' during build '%s': %w", e.EnvID, e.BuildID, err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	err = e.MoveToEnvDir(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("error moving env files to their final destination during while building env '%s' during build '%s': %w", e.EnvID, e.BuildID, err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	return nil
}

func (e *Env) initialize(ctx context.Context, tracer trace.Tracer) error {
	childCtx, childSpan := tracer.Start(ctx, "initialize")
	defer childSpan.End()

	var err error
	defer func() {
		if err != nil {
			e.Cleanup(childCtx, tracer)
		}
	}()

	err = os.MkdirAll(e.tmpBuildDirPath(), 0o777)
	if err != nil {
		errMsg := fmt.Errorf("error creating tmp build dir: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "created tmp build dir")

	err = os.WriteFile(e.tmpBuildIDFilePath(), []byte(e.BuildID), 0o644)
	if err != nil {
		errMsg := fmt.Errorf("error writing build ID file: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "wrote build ID")

	return nil
}

func (e *Env) MoveToEnvDir(ctx context.Context, tracer trace.Tracer) error {
	childCtx, childSpan := tracer.Start(ctx, "move-to-env-dir")
	defer childSpan.End()

	err := os.Rename(e.tmpSnapfilePath(), e.envSnapfilePath())
	if err != nil {
		errMsg := fmt.Errorf("error moving snapshot file: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "moved snapshot file")

	err = os.Rename(e.tmpMemfilePath(), e.envMemfilePath())
	if err != nil {
		errMsg := fmt.Errorf("error moving memfile: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "moved memfile")

	err = os.Rename(e.tmpRootfsPath(), e.envRootfsPath())
	if err != nil {
		errMsg := fmt.Errorf("error moving rootfs: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "moved rootfs")

	err = os.Rename(e.tmpBuildIDFilePath(), e.envBuildIDFilePath())
	if err != nil {
		errMsg := fmt.Errorf("error moving build ID: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "moved build ID")

	return nil
}

func (e *Env) Cleanup(ctx context.Context, tracer trace.Tracer) {
	childCtx, childSpan := tracer.Start(ctx, "cleanup")
	defer childSpan.End()

	err := os.RemoveAll(e.tmpBuildDirPath())
	if err != nil {
		errMsg := fmt.Errorf("error cleaning up env files %w", err)
		telemetry.ReportError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "cleaned up env files")
	}
}
