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

// The metadata serialization should not be changed — it is different from the field names we use here!
type MmdsMetadata struct {
	SandboxId            string `json:"instanceID"`
	TemplateId           string `json:"envID"`
	LogsCollectorAddress string `json:"address"`
	TraceId              string `json:"traceID"`
	TeamId               string `json:"teamID"`
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
	firecrackerSocketPath,
	uffdSocketPath string,
	metadata interface{},
	snapfile *storage.SimpleFile,
	uffdReady chan struct{},
) error {
	childCtx, childSpan := tracer.Start(ctx, "load-snapshot", trace.WithAttributes(
		attribute.String("sandbox.socket.path", firecrackerSocketPath),
	))
	defer childSpan.End()

	client := client.NewHTTPClient(strfmt.NewFormats())
	transport := firecracker.NewUnixSocketTransport(firecrackerSocketPath, nil, false)
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
	rootfs *storage.OverlayFile,
	uffdReady chan struct{},
) (*fc, error) {
	childCtx, childSpan := tracer.Start(ctx, "initialize-fc", trace.WithAttributes(
		attribute.String("sandbox.id", mmdsMetadata.SandboxId),
		attribute.Int("sandbox.slot.index", slot.SlotIdx),
	))
	defer childSpan.End()

	vmmCtx, _ := tracer.Start(
		trace.ContextWithSpanContext(context.Background(), childSpan.SpanContext()),
		"fc-vmm",
	)

	rootfsPath, err := rootfs.Path()
	if err != nil {
		return nil, fmt.Errorf("error getting rootfs path: %w", err)
	}

	rootfsMountCmd := fmt.Sprintf(
		"mkdir -p %s && touch %s && mount -o bind %s %s && ",
		files.BuildDir(),
		files.BuildRootfsPath(),
		rootfsPath,
		files.BuildRootfsPath(),
	)

	kernelMountCmd := fmt.Sprintf(
		"mkdir -p %s && touch %s && mount -o bind %s %s && ",
		files.BuildKernelDir(),
		files.BuildKernelPath(),
		files.CacheKernelPath(),
		files.BuildKernelPath(),
	)

	fcCmd := fmt.Sprintf("%s --api-sock %s", files.FirecrackerPath(), files.SandboxFirecrackerSocketPath())
	inNetNSCmd := fmt.Sprintf("ip netns exec %s ", slot.NamespaceID())

	telemetry.SetAttributes(childCtx,
		attribute.String("sandbox.fc.cmd", fcCmd),
		attribute.String("sandbox.netns.cmd", inNetNSCmd),
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
	}, nil
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
			// line := scanner.Text()

			// msg := fmt.Sprintf("[firecracker stdout]: %s — %v", fc.metadata.SandboxId, line)

			// telemetry.ReportEvent(fc.ctx, msg,
			// 	attribute.String("type", "stdout"),
			// )
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			errMsg := fmt.Errorf("error reading vmm stdout: %w", readerErr)
			telemetry.ReportError(fc.ctx, errMsg)

			fmt.Fprintf(os.Stderr, "[firecracker stdout error]: %s — %v\n", fc.metadata.SandboxId, errMsg)
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

			msg := fmt.Sprintf("[firecracker stderr]: %s — %v", fc.metadata.SandboxId, line)

			// telemetry.ReportEvent(fc.ctx, msg,
			// 	attribute.String("type", "stderr"),
			// )

			fmt.Fprintln(os.Stderr, msg)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			errMsg := fmt.Errorf("error closing vmm stderr reader: %w", readerErr)
			telemetry.ReportError(fc.ctx, errMsg)
			fmt.Fprintf(os.Stderr, "[firecracker stderr error]: %s — %v\n", fc.metadata.SandboxId, errMsg)
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
		attribute.String("sandbox.cmd", fc.cmd.String()),
		attribute.String("sandbox.cmd.dir", fc.cmd.Dir),
		attribute.String("sandbox.cmd.path", fc.cmd.Path),
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
