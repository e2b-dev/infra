package build

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/template"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"go.opentelemetry.io/otel/trace"

	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Env struct {
	templateStorage.TemplateFiles

	// Command to run when building the env.
	StartCmd string

	// Path to the firecracker binary.
	FirecrackerBinaryPath string

	// The number of vCPUs to allocate to the VM.
	VCpuCount int64

	// The amount of RAM memory to allocate to the VM, in MiB.
	MemoryMB int64

	// The amount of free disk to allocate to the VM, in MiB.
	DiskSizeMB int64

	// Path to the directory where the temporary files for the build are stored.
	BuildLogsWriter io.Writer

	// Real size of the rootfs after building the env.
	rootfsSize int64

	// Version of the kernel.
	KernelVersion string

	// Whether to use hugepages or not.
	HugePages bool
}

//go:embed provision.sh
var provisionEnvScriptFile string
var EnvInstanceTemplate = template.Must(template.New("provisioning-script").Parse(provisionEnvScriptFile))

// Path to the directory where the kernel is stored.
func (e *Env) KernelDirPath() string {
	return filepath.Join(templateStorage.KernelsDir, e.KernelVersion)
}

// Real size in MB of rootfs after building the env
func (e *Env) RootfsSizeMB() int64 {
	return e.rootfsSize >> 20
}

// Path to the directory where the kernel can be accessed inside when the dirs are mounted.
func (e *Env) KernelMountedPath() string {
	return filepath.Join(templateStorage.KernelMountDir, templateStorage.KernelName)
}

func (e *Env) Build(ctx context.Context, tracer trace.Tracer, docker *client.Client, legacyDocker *docker.Client) error {
	childCtx, childSpan := tracer.Start(ctx, "build")
	defer childSpan.End()

	err := os.MkdirAll(e.BuildDir(), 0o777)
	if err != nil {
		errMsg := fmt.Errorf("error initializing directories for building env '%s' during build '%s': %w", e.TemplateId, e.BuildId, err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	rootfs, err := NewRootfs(childCtx, tracer, e, docker, legacyDocker)
	if err != nil {
		errMsg := fmt.Errorf("error creating rootfs for env '%s' during build '%s': %w", e.TemplateId, e.BuildId, err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	network, err := NewFCNetwork(childCtx, tracer, e)
	if err != nil {
		errMsg := fmt.Errorf("error network setup for FC while building env '%s' during build '%s': %w", e.TemplateId, e.BuildId, err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	defer network.Cleanup(childCtx, tracer)

	_, err = NewSnapshot(childCtx, tracer, e, network, rootfs)
	if err != nil {
		errMsg := fmt.Errorf("error snapshot for env '%s' during build '%s': %w", e.TemplateId, e.BuildId, err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	return nil
}

func (e *Env) Remove(ctx context.Context, tracer trace.Tracer) error {
	childCtx, childSpan := tracer.Start(ctx, "move-to-env-dir")
	defer childSpan.End()

	err := os.RemoveAll(e.BuildDir())
	if err != nil {
		errMsg := fmt.Errorf("error removing build dir: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "removed build dir")

	return nil
}
