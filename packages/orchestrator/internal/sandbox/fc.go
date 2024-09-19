package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client/operations"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/go-openapi/strfmt"
)

type MmdsMetadata struct {
	InstanceID string `json:"instanceID"`
	EnvID      string `json:"envID"`
	Address    string `json:"address"`
	TraceID    string `json:"traceID"`
	TeamID     string `json:"teamID"`
}

type fc struct {
	uffdReady chan struct{}
	snapfile  *storage.SimpleFile

	ctx context.Context

	cmd *exec.Cmd

	stdout *io.PipeReader
	stderr *io.PipeReader

	metadata *MmdsMetadata

	uffdSocketPath        string
	firecrackerSocketPath string
}

func (fc *fc) wait() error {
	err := fc.cmd.Wait()
	if err != nil {
		return fmt.Errorf("error waiting for fc process: %w", err)
	}

	return nil
}

func (fc *fc) loadSnapshot(
	ctx context.Context,
	tracer trace.Tracer,
	firecrakcerSocketPath,
	uffdSocketPath string,
	metadata interface{},
	snapfile *storage.SimpleFile,
	uffdReady chan struct{},
) error {
	childCtx, childSpan := tracer.Start(ctx, "load-snapshot", trace.WithAttributes(
		attribute.String("instance.socket.path", firecrakcerSocketPath),
	))
	defer childSpan.End()

	client := client.NewHTTPClient(strfmt.NewFormats())
	transport := firecracker.NewUnixSocketTransport(firecrakcerSocketPath, nil, false)
	client.SetTransport(transport)

	telemetry.ReportEvent(childCtx, "created FC socket client")

	var backend *models.MemoryBackend

	err := waitForSocket(childCtx, uffdSocketPath)
	if err != nil {
		telemetry.ReportCriticalError(childCtx, err)

		return err
	}

	telemetry.ReportEvent(childCtx, "uffd socket ready")

	snapfilePath, err := snapfile.Ensure()
	if err != nil {
		return fmt.Errorf("error ensuring snapfile: %w", err)
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
			SnapshotPath:        &snapfilePath,
		},
	}

	_, err = client.Operations.LoadSnapshot(&snapshotConfig)
	if err != nil {
		errMsg := fmt.Errorf("error loading snapshot: %w", err)

		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	if uffdReady != nil {
		select {
		case <-childCtx.Done():
			return childCtx.Err()
		case <-uffdReady:
			telemetry.ReportEvent(childCtx, "uffd polling ready")

			break
		}
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
		errMsg := fmt.Errorf("error pausing vm: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "snapshot loaded")

	mmdsConfig := operations.PutMmdsParams{
		Context: childCtx,
		Body:    metadata,
	}

	_, err = client.Operations.PutMmds(&mmdsConfig)
	if err != nil {
		errMsg := fmt.Errorf("error setting mmds data: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "mmds data set")

	return nil
}

func NewFC(
	ctx context.Context,
	tracer trace.Tracer,
	slot IPSlot,
	files *templateStorage.SandboxFiles,
	mmdsMetadata *MmdsMetadata,
	snapfile *storage.SimpleFile,
	uffdReady chan struct{},
) *fc {
	childCtx, childSpan := tracer.Start(ctx, "initialize-fc", trace.WithAttributes(
		attribute.String("instance.id", mmdsMetadata.InstanceID),
		attribute.Int("instance.slot.index", slot.SlotIdx),
	))
	defer childSpan.End()

	vmmCtx, _ := tracer.Start(
		trace.ContextWithSpanContext(context.Background(), childSpan.SpanContext()),
		"fc-vmm",
	)

	rootfsMountCmd := fmt.Sprintf(
		"mkdir -p %s && touch %s && mount -o bind %s %s && ",
		files.BuildDir(),
		files.BuildRootfsPath(),
		files.SandboxCacheRootfsPath(),
		files.BuildRootfsPath(),
	)

	kernelMountCmd := fmt.Sprintf(
		"mkdir -p %s && touch %s && mount -o bind,ro %s %s && ",
		files.BuildKernelDir(),
		files.BuildKernelPath(),
		files.CacheKernelPath(),
		files.BuildKernelPath(),
	)

	fcCmd := fmt.Sprintf("%s --api-sock %s", files.FirecrackerPath(), files.SandboxFirecrackerSocketPath())
	inNetNSCmd := fmt.Sprintf("ip netns exec %s ", slot.NamespaceID())

	telemetry.SetAttributes(childCtx,
		attribute.String("instance.firecracker.command", fcCmd),
		attribute.String("instance.netns.command", inNetNSCmd),
	)

	cmd := exec.Command(
		"unshare",
		"-pfmiC",
		"--kill-child",
		"--",
		"bash",
		"-c",
		rootfsMountCmd+kernelMountCmd+inNetNSCmd+fcCmd,
	)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create a new session
	}

	cmdStdoutReader, cmdStdoutWriter := io.Pipe()
	cmdStderrReader, cmdStderrWriter := io.Pipe()

	cmd.Stderr = cmdStdoutWriter
	cmd.Stdout = cmdStderrWriter

	return &fc{
		uffdReady:             uffdReady,
		cmd:                   cmd,
		stdout:                cmdStdoutReader,
		stderr:                cmdStderrReader,
		ctx:                   vmmCtx,
		firecrackerSocketPath: files.SandboxFirecrackerSocketPath(),
		metadata:              mmdsMetadata,
		uffdSocketPath:        files.SandboxUffdSocketPath(),
		snapfile:              snapfile,
	}
}

func (fc *fc) start(
	ctx context.Context,
	tracer trace.Tracer,
) error {
	childCtx, childSpan := tracer.Start(ctx, "start-fc")
	defer childSpan.End()

	go func() {
		defer func() {
			readerErr := fc.stdout.Close()
			if readerErr != nil {
				errMsg := fmt.Errorf("error closing vmm stdout reader: %w", readerErr)
				telemetry.ReportError(fc.ctx, errMsg)
			}
		}()

		scanner := bufio.NewScanner(fc.stdout)

		for scanner.Scan() {
			line := scanner.Text()

			telemetry.ReportEvent(fc.ctx, "vmm log",
				attribute.String("type", "stdout"),
				attribute.String("message", line),
			)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			errMsg := fmt.Errorf("error reading vmm stdout: %w", readerErr)
			telemetry.ReportError(fc.ctx, errMsg)
			fmt.Fprintf(os.Stderr, "[firecracker stdout error]: %s — %v\n", fc.metadata.InstanceID, errMsg)
		} else {
			telemetry.ReportEvent(fc.ctx, "vmm stdout reader closed")
		}
	}()

	go func() {
		defer func() {
			readerErr := fc.stderr.Close()
			if readerErr != nil {
				errMsg := fmt.Errorf("error closing vmm stdout reader: %w", readerErr)
				telemetry.ReportError(fc.ctx, errMsg)
			}
		}()

		scanner := bufio.NewScanner(fc.stderr)

		for scanner.Scan() {
			line := scanner.Text()

			telemetry.ReportEvent(fc.ctx, "vmm log",
				attribute.String("type", "stderr"),
				attribute.String("message", line),
			)

			fmt.Fprintf(os.Stderr, "[firecracker stderr]: %s — %v\n", fc.metadata.InstanceID, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			errMsg := fmt.Errorf("error closing vmm stderr reader: %w", readerErr)
			telemetry.ReportError(fc.ctx, errMsg)
			fmt.Fprintf(os.Stderr, "[firecracker stderr error]: %s — %v\n", fc.metadata.InstanceID, errMsg)
		} else {
			telemetry.ReportEvent(fc.ctx, "vmm stderr reader closed")
		}
	}()

	err := fc.cmd.Start()
	if err != nil {
		errMsg := fmt.Errorf("error starting fc process: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "started fc process")

	// Wait for the FC process to start so we can use FC API
	err = waitForSocket(childCtx, fc.firecrackerSocketPath)
	if err != nil {
		errMsg := fmt.Errorf("error waiting for fc socket: %w", err)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "fc process created socket")

	if loadErr := fc.loadSnapshot(
		childCtx,
		tracer,
		fc.firecrackerSocketPath,
		fc.uffdSocketPath,
		fc.metadata,
		fc.snapfile,
		fc.uffdReady,
	); loadErr != nil {
		fcErr := fc.stop()

		errMsg := fmt.Errorf("failed to load snapshot: %w", errors.Join(loadErr, fcErr))
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "loaded snapshot")

	defer func() {
		if err != nil {
			err := fc.stop()
			if err != nil {
				errMsg := fmt.Errorf("error stopping FC process: %w", err)

				telemetry.ReportError(childCtx, errMsg)
			}
		}
	}()

	telemetry.SetAttributes(
		childCtx,
		attribute.String("instance.socket.path", fc.firecrackerSocketPath),
		attribute.String("instance.env.id", fc.metadata.EnvID),
		attribute.String("instance.cmd", fc.cmd.String()),
		attribute.String("instance.cmd.dir", fc.cmd.Dir),
		attribute.String("instance.cmd.path", fc.cmd.Path),
	)

	return nil
}

func (fc *fc) stop() error {
	err := fc.cmd.Process.Kill()
	if err != nil {
		return fmt.Errorf("failed to send KILL to FC process: %w", err)
	}

	return nil
}
