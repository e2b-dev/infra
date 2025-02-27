package fc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	txtTemplate "text/template"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/socket"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const startScript = `mount --make-rprivate / &&
mount -t tmpfs tmpfs {{ .buildDir }} -o X-mount.mkdir &&
mount -t tmpfs tmpfs {{ .buildKernelDir }} -o X-mount.mkdir &&
ln -s {{ .rootfsPath }} {{ .buildRootfsPath }} &&
ln -s {{ .kernelPath }} {{ .buildKernelPath }} &&
ip netns exec {{ .namespaceID }} {{ .firecrackerPath }} --api-sock {{ .firecrackerSocket }}`

var startScriptTemplate = txtTemplate.Must(txtTemplate.New("fc-start").Parse(startScript))

type Process struct {
	uffdReady chan struct{}
	snapfile  template.File

	cmd *exec.Cmd

	metadata *MmdsMetadata

	uffdSocketPath        string
	firecrackerSocketPath string

	rootfs *rootfs.CowDevice
	files  *storage.SandboxFiles

	Exit chan error

	client *apiClient
}

func NewProcess(
	ctx context.Context,
	tracer trace.Tracer,
	slot network.Slot,
	files *storage.SandboxFiles,
	mmdsMetadata *MmdsMetadata,
	snapfile template.File,
	rootfs *rootfs.CowDevice,
	uffdReady chan struct{},
	baseTemplateID string,
) (*Process, error) {
	childCtx, childSpan := tracer.Start(ctx, "initialize-fc", trace.WithAttributes(
		attribute.String("sandbox.id", mmdsMetadata.SandboxId),
		attribute.Int("sandbox.slot.index", slot.Idx),
	))
	defer childSpan.End()

	var fcStartScript bytes.Buffer

	baseBuild := storage.NewTemplateFiles(
		baseTemplateID,
		rootfs.BaseBuildId,
		files.KernelVersion,
		files.FirecrackerVersion,
		files.Hugepages(),
	)

	err := startScriptTemplate.Execute(&fcStartScript, map[string]interface{}{
		"rootfsPath":        files.SandboxCacheRootfsLinkPath(),
		"kernelPath":        files.CacheKernelPath(),
		"buildDir":          baseBuild.BuildDir(),
		"buildRootfsPath":   baseBuild.BuildRootfsPath(),
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

	return &Process{
		Exit:                  make(chan error, 1),
		uffdReady:             uffdReady,
		cmd:                   cmd,
		firecrackerSocketPath: files.SandboxFirecrackerSocketPath(),
		metadata:              mmdsMetadata,
		uffdSocketPath:        files.SandboxUffdSocketPath(),
		snapfile:              snapfile,
		client:                newApiClient(files.SandboxFirecrackerSocketPath()),
		rootfs:                rootfs,
		files:                 files,
	}, nil
}

func (p *Process) Start(
	ctx context.Context,
	tracer trace.Tracer,
	logger *logs.SandboxLogger,
) error {
	childCtx, childSpan := tracer.Start(ctx, "start-fc")
	defer childSpan.End()

	stdoutReader, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("error creating fc stdout pipe: %w", err)
	}

	go func() {
		// The stdout should be closed with the process cmd automatically, as it uses the StdoutPipe()
		// TODO: Better handling of processing all logs before calling wait
		scanner := bufio.NewScanner(stdoutReader)

		for scanner.Scan() {
			line := scanner.Text()

			logger.Infof("[sandbox %s]: stdout: %s\n", p.metadata.SandboxId, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			logger.Errorf("[sandbox %s]: error reading fc stdout: %v\n", p.metadata.SandboxId, readerErr)
		}
	}()

	stderrReader, err := p.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("error creating fc stderr pipe: %w", err)
	}

	go func() {
		// The stderr should be closed with the process cmd automatically, as it uses the StderrPipe()
		// TODO: Better handling of processing all logs before calling wait
		scanner := bufio.NewScanner(stderrReader)

		for scanner.Scan() {
			line := scanner.Text()

			logger.Warnf("[sandbox %s]: stderr: %s\n", p.metadata.SandboxId, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			logger.Errorf("[sandbox %s]: error reading fc stderr: %v\n", p.metadata.SandboxId, readerErr)
		}
	}()

	err = os.Symlink("/dev/null", p.files.SandboxCacheRootfsLinkPath())
	if err != nil {
		return fmt.Errorf("error symlinking rootfs: %w", err)
	}

	err = p.cmd.Start()
	if err != nil {
		return fmt.Errorf("error starting fc process: %w", err)
	}

	startCtx, cancelStart := context.WithCancelCause(childCtx)
	defer cancelStart(fmt.Errorf("fc finished starting"))

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

			cancelStart(errMsg)

			return
		}

		p.Exit <- nil
	}()

	// Wait for the FC process to start so we can use FC API
	err = socket.Wait(startCtx, p.firecrackerSocketPath)
	if err != nil {
		errMsg := fmt.Errorf("error waiting for fc socket: %w", err)

		fcStopErr := p.Stop()

		return errors.Join(errMsg, fcStopErr)
	}

	device, err := p.rootfs.Path()
	if err != nil {
		return fmt.Errorf("error getting rootfs path: %w", err)
	}

	err = os.Remove(p.files.SandboxCacheRootfsLinkPath())
	if err != nil {
		return fmt.Errorf("error removing rootfs symlink: %w", err)
	}

	err = os.Symlink(device, p.files.SandboxCacheRootfsLinkPath())
	if err != nil {
		return fmt.Errorf("error symlinking rootfs: %w", err)
	}

	err = p.client.loadSnapshot(
		startCtx,
		p.uffdSocketPath,
		p.uffdReady,
		p.snapfile,
	)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error loading snapshot: %w", err), fcStopErr)
	}

	err = p.client.resumeVM(startCtx)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error resuming vm: %w", err), fcStopErr)
	}

	err = p.client.setMmds(startCtx, p.metadata)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error setting mmds: %w", err), fcStopErr)
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
	if p.cmd.Process == nil {
		return fmt.Errorf("fc process not started")
	}

	err := p.cmd.Process.Kill()
	if err != nil {
		return fmt.Errorf("failed to send KILL to FC process: %w", err)
	}

	return nil
}

func (p *Process) Pause(ctx context.Context, tracer trace.Tracer) error {
	ctx, childSpan := tracer.Start(ctx, "pause-fc")
	defer childSpan.End()

	return p.client.pauseVM(ctx)
}

// VM needs to be paused before creating a snapshot.
func (p *Process) CreateSnapshot(ctx context.Context, tracer trace.Tracer, snapfilePath string, memfilePath string) error {
	ctx, childSpan := tracer.Start(ctx, "create-snapshot-fc")
	defer childSpan.End()

	return p.client.createSnapshot(ctx, snapfilePath, memfilePath)
}
