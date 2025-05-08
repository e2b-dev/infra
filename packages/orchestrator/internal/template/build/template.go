package build

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"text/template"
	"time"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Env struct {
	*storage.TemplateFiles

	// Command to run when building the env.
	StartCmd string

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

	// HugePages sets whether the VM use huge pages.
	HugePages bool
}

//go:embed provision.sh
var provisionEnvScriptFile string
var EnvInstanceTemplate = template.Must(template.New("provisioning-script").Parse(provisionEnvScriptFile))

// Real size in MB of rootfs after building the env
func (e *Env) RootfsSizeMB() int64 {
	return e.rootfsSize >> 20
}

func (e *Env) Build(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	docker *client.Client,
	legacyDocker *docker.Client,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	clientID string,
	envdVersion string,
) (*sandbox.Sandbox, error) {
	childCtx, childSpan := tracer.Start(ctx, "build")
	defer childSpan.End()

	err := os.MkdirAll(e.BuildDir(), 0o777)
	if err != nil {
		errMsg := fmt.Errorf("error initializing directories for building env '%s' during build '%s': %w", e.TemplateId, e.BuildId, err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	_, err = NewRootfs(childCtx, tracer, postProcessor, e, docker, legacyDocker)
	if err != nil {
		errMsg := fmt.Errorf("error creating rootfs for env '%s' during build '%s': %w", e.TemplateId, e.BuildId, err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	config := &orchestrator.SandboxConfig{
		TemplateId:         e.TemplateId,
		BuildId:            e.BuildId,
		KernelVersion:      e.KernelVersion,
		FirecrackerVersion: e.FirecrackerVersion,
		HugePages:          e.HugePages,
		SandboxId:          uuid.New().String(),

		EnvdVersion: envdVersion,
		Vcpu:        e.VCpuCount,
		RamMb:       e.MemoryMB,
		// TODO: set proper teamID
		TeamId: uuid.New().String(),

		BaseTemplateId: uuid.New().String(),
	}

	rootfs, err := block.NewLocal(e.BuildRootfsPath(), e.RootfsBlockSize())
	if err != nil {
		errMsg := fmt.Errorf("error reading rootfs blocks: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	// Create empty memfile in the size of the RAM
	emptyMemoryFile, err := os.Create(e.BuildMemfilePath())
	if err != nil {
		errMsg := fmt.Errorf("error creating blank memfile: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}
	defer emptyMemoryFile.Close()
	emptyMemoryFile.Truncate(config.RamMb * 1024 * 1024)
	emptyMemoryFile.Close()

	memfile, err := block.NewLocal(e.BuildMemfilePath(), e.MemfilePageSize())
	if err != nil {
		errMsg := fmt.Errorf("error reading memfile pages: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	sbx, cleanup, err := sandbox.CreateSandbox(
		childCtx,
		tracer,
		networkPool,
		devicePool,
		config,
		rootfs,
		memfile,
		// TODO: set correct sandbox timeout
		time.Hour,
		clientID,
	)
	if err != nil {
		errMsg := fmt.Errorf("error creating sandbox: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		err := cleanup.Run(childCtx)

		return nil, errors.Join(errMsg, err)
	}

	return sbx, nil
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

func (e *Env) MemfilePageSize() int64 {
	if e.HugePages {
		return header.HugepageSize
	}

	return header.PageSize
}

func (e *Env) RootfsBlockSize() int64 {
	return header.RootfsBlockSize
}
