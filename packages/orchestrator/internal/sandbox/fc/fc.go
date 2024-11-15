package fc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"text/template"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/go-openapi/strfmt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	localStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/local_storage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/socket"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client/operations"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// The metadata serialization should not be changed — it is different from the field names we use here!
type MmdsMetadata struct {
	SandboxId            string `json:"instanceID"`
	TemplateId           string `json:"envID"`
	LogsCollectorAddress string `json:"address"`
	TraceId              string `json:"traceID"`
	TeamId               string `json:"teamID"`
}

type Process struct {
	uffdReady chan struct{}
	snapfile  *localStorage.File

	cmd *exec.Cmd

	stdout *io.PipeReader
	stderr *io.PipeReader

	metadata *MmdsMetadata

	uffdSocketPath        string
	firecrackerSocketPath string
	rootfsPath            string

	Exit chan error
}

func (p *Process) loadSnapshot(
	ctx context.Context,
	tracer trace.Tracer,
	firecrackerSocketPath,
	uffdSocketPath string,
	metadata interface{},
	snapfile *localStorage.File,
	uffdReady chan struct{},
) error {
	childCtx, childSpan := tracer.Start(ctx, "load-snapshot", trace.WithAttributes(
		attribute.String("sandbox.socket.path", firecrackerSocketPath),
	))
	defer childSpan.End()

	client := client.NewHTTPClient(strfmt.NewFormats())
	transport := firecracker.NewUnixSocketTransport(firecrackerSocketPath, nil, false)
	client.SetTransport(transport)

	var backend *models.MemoryBackend

	err := socket.Wait(childCtx, uffdSocketPath)
	if err != nil {
		return fmt.Errorf("error waiting for uffd socket: %w", err)
	}

	backendType := models.MemoryBackendBackendTypeUffd
	backend = &models.MemoryBackend{
		BackendPath: &uffdSocketPath,
		BackendType: &backendType,
	}

	snapshotConfig := operations.LoadSnapshotParams{
		Context: childCtx,
		Body: &models.SnapshotLoadParams{
			ResumeVM:            false,
			EnableDiffSnapshots: false,
			MemBackend:          backend,
			SnapshotPath:        &snapfile.Path,
		},
	}

	_, err = client.Operations.LoadSnapshot(&snapshotConfig)
	if err != nil {
		return fmt.Errorf("error loading snapshot: %w", err)
	}

	select {
	case <-childCtx.Done():
		return fmt.Errorf("context canceled while waiting for uffd ready: %w", errors.Join(childCtx.Err(), context.Cause(childCtx)))
	case <-uffdReady:
	}

	state := models.VMStateResumed
	pauseConfig := operations.PatchVMParams{
		Context: childCtx,
		Body: &models.VM{
			State: &state,
		},
	}

	_, err = client.Operations.PatchVM(&pauseConfig)
	if err != nil {
		return fmt.Errorf("error resuming vm: %w", errors.Join(err, context.Cause(childCtx)))
	}

	mmdsConfig := operations.PutMmdsParams{
		Context: childCtx,
		Body:    metadata,
	}

	_, err = client.Operations.PutMmds(&mmdsConfig)
	if err != nil {
		return fmt.Errorf("error setting mmds data: %w", err)
	}

	return nil
}

const fcStartScript = `mount --make-rprivate / &&
mount -t tmpfs tmpfs {{ .buildDir }} -o X-mount.mkdir &&
mount -t tmpfs tmpfs {{ .buildKernelDir }} -o X-mount.mkdir &&
ln -s {{ .rootfsPath }} {{ .buildRootfsPath }} &&
ln -s {{ .kernelPath }} {{ .buildKernelPath }} &&
ip netns exec {{ .namespaceID }} {{ .firecrackerPath }} --api-sock {{ .firecrackerSocket }}`

var fcStartScriptTemplate = template.Must(template.New("fc-start").Parse(fcStartScript))

func NewProcess(
	ctx context.Context,
	tracer trace.Tracer,
	slot network.IPSlot,
	files *templateStorage.SandboxFiles,
	mmdsMetadata *MmdsMetadata,
	snapfile *localStorage.File,
	rootfs *localStorage.RootfsOverlay,
	uffdReady chan struct{},
) (*Process, error) {
	childCtx, childSpan := tracer.Start(ctx, "initialize-fc", trace.WithAttributes(
		attribute.String("sandbox.id", mmdsMetadata.SandboxId),
		attribute.Int("sandbox.slot.index", slot.SlotIdx),
	))
	defer childSpan.End()

	rootfsPath, err := rootfs.Path(childCtx)
	if err != nil {
		return nil, fmt.Errorf("error getting rootfs path: %w", err)
	}

	var fcStartScript bytes.Buffer

	err = fcStartScriptTemplate.Execute(&fcStartScript, map[string]interface{}{
		"rootfsPath":        rootfsPath,
		"kernelPath":        files.CacheKernelPath(),
		"buildDir":          files.BuildDir(),
		"buildRootfsPath":   files.BuildRootfsPath(),
		"buildKernelPath":   files.BuildKernelPath(),
		"buildKernelDir":    files.BuildKernelDir(),
		"namespaceID":       slot.NamespaceID(),
		"firecrackerPath":   files.FirecrackerPath(),
		"firecrackerSocket": files.SandboxFirecrackerSocketPath(),
	})
	if err != nil {
		return nil, fmt.Errorf("error executing fc start script template: %w", err)
	}

	telemetry.SetAttributes(childCtx,
		attribute.String("sandbox.cmd", fcStartScript.String()),
	)

	cmd := exec.Command(
		"unshare",
		"-pfm",
		"--kill-child",
		"--",
		"bash",
		"-c",
		fcStartScript.String(),
	)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create a new session
	}

	cmdStdoutReader, cmdStdoutWriter := io.Pipe()
	cmdStderrReader, cmdStderrWriter := io.Pipe()

	cmd.Stderr = cmdStdoutWriter
	cmd.Stdout = cmdStderrWriter

	return &Process{
		Exit:                  make(chan error, 1),
		uffdReady:             uffdReady,
		rootfsPath:            rootfsPath,
		cmd:                   cmd,
		stdout:                cmdStdoutReader,
		stderr:                cmdStderrReader,
		firecrackerSocketPath: files.SandboxFirecrackerSocketPath(),
		metadata:              mmdsMetadata,
		uffdSocketPath:        files.SandboxUffdSocketPath(),
		snapfile:              snapfile,
	}, nil
}

func (p *Process) Start(
	ctx context.Context,
	tracer trace.Tracer,
	logger *logs.SandboxLogger,
) error {
	childCtx, childSpan := tracer.Start(ctx, "start-fc")
	defer childSpan.End()

	go func() {
		defer func() {
			readerErr := p.stdout.Close()
			if readerErr != nil {
				logger.Errorf("[sandbox %s]: error closing fc stdout reader: %v\n", p.metadata.SandboxId, readerErr)
			}
		}()

		scanner := bufio.NewScanner(p.stdout)

		for scanner.Scan() {
			line := scanner.Text()

			logger.Infof("[sandbox %s]: stdout: %s\n", p.metadata.SandboxId, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			logger.Errorf("[sandbox %s]: error reading fc stdout: %v\n", p.metadata.SandboxId, readerErr)
		}
	}()

	go func() {
		defer func() {
			readerErr := p.stderr.Close()
			if readerErr != nil {
				logger.Errorf("[sandbox %s]: error closing fc stderr reader: %v\n", p.metadata.SandboxId, readerErr)
			}
		}()

		scanner := bufio.NewScanner(p.stderr)

		for scanner.Scan() {
			line := scanner.Text()

			logger.Warnf("[sandbox %s]: stderr: %s\n", p.metadata.SandboxId, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			logger.Errorf("[sandbox %s]: error reading fc stderr: %v\n", p.metadata.SandboxId, readerErr)
		}
	}()

	err := p.cmd.Start()
	if err != nil {
		return fmt.Errorf("error starting fc process: %w", err)
	}

	fcStartCtx, cancelFcStart := context.WithCancelCause(childCtx)
	defer cancelFcStart(fmt.Errorf("fc finished starting"))

	go func() {
		waitErr := p.cmd.Wait()
		if waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				// Check if the process was killed by a signal
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGKILL {
					p.Exit <- nil

					return
				}
			}

			errMsg := fmt.Errorf("error waiting for fc process: %w", waitErr)

			p.Exit <- errMsg

			cancelFcStart(errMsg)

			return
		}

		p.Exit <- nil
	}()

	// Wait for the FC process to start so we can use FC API
	err = socket.Wait(fcStartCtx, p.firecrackerSocketPath)
	if err != nil {
		errMsg := fmt.Errorf("error waiting for fc socket: %w", err)

		fcStopErr := p.Stop()

		return errors.Join(errMsg, fcStopErr)
	}

	err = p.loadSnapshot(
		fcStartCtx,
		tracer,
		p.firecrackerSocketPath,
		p.uffdSocketPath,
		p.metadata,
		p.snapfile,
		p.uffdReady,
	)
	if err != nil {
		errMsg := fmt.Errorf("failed to load snapshot: %w", err)

		fcStopErr := p.Stop()

		return errors.Join(errMsg, fcStopErr)
	}

	telemetry.SetAttributes(
		childCtx,
		attribute.String("sandbox.cmd.dir", p.cmd.Dir),
		attribute.String("sandbox.cmd.path", p.cmd.Path),
	)

	return nil
}

func (p *Process) Pid() (int, error) {
	if p.cmd.Process == nil {
		return 0, fmt.Errorf("fc process not started")
	}

	return p.cmd.Process.Pid, nil
}

func (p *Process) Stop() error {
	err := p.cmd.Process.Kill()
	if err != nil {
		return fmt.Errorf("failed to send KILL to FC process: %w", err)
	}

	return nil
}
