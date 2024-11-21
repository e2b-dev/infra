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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/socket"
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

var startScriptTemplate = template.Must(template.New("fc-start").Parse(startScript))

type Process struct {
	uffdReady chan struct{}
	snapfile  *cache.File

	cmd *exec.Cmd

	stdout *io.PipeReader
	stderr *io.PipeReader

	metadata *MmdsMetadata

	uffdSocketPath        string
	firecrackerSocketPath string
	rootfs                *cache.RootfsOverlay

	Exit chan error
}

func NewProcess(
	ctx context.Context,
	tracer trace.Tracer,
	slot network.Slot,
	files *storage.SandboxFiles,
	mmdsMetadata *MmdsMetadata,
	snapfile *cache.File,
	rootfs *cache.RootfsOverlay,
	uffdReady chan struct{},
) (*Process, error) {
	childCtx, childSpan := tracer.Start(ctx, "initialize-fc", trace.WithAttributes(
		attribute.String("sandbox.id", mmdsMetadata.SandboxId),
		attribute.Int("sandbox.slot.index", slot.Idx),
	))
	defer childSpan.End()

	rootfsPath, err := rootfs.Path(childCtx)
	if err != nil {
		return nil, fmt.Errorf("error getting rootfs path: %w", err)
	}

	var fcStartScript bytes.Buffer

	err = startScriptTemplate.Execute(&fcStartScript, map[string]interface{}{
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
	cmd.Stdout = cmdStdoutWriter

	cmdStderrReader, cmdStderrWriter := io.Pipe()
	cmd.Stderr = cmdStderrWriter

	return &Process{
		Exit:                  make(chan error, 1),
		uffdReady:             uffdReady,
		rootfs:                rootfs,
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

	client := newApiClient(p.firecrackerSocketPath)

	err = client.loadSnapshot(
		startCtx,
		p.uffdSocketPath,
		p.uffdReady,
		p.snapfile,
		p.rootfs,
	)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error loading snapshot: %w", err), fcStopErr)
	}

	err = client.resumeVM(startCtx)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error resuming vm: %w", err), fcStopErr)
	}

	err = client.setMmds(startCtx, p.metadata)
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