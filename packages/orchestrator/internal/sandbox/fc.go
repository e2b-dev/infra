package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/go-openapi/strfmt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client/operations"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	uffdPollingTimeout = 10 * time.Second
)

type MmdsMetadata struct {
	InstanceID string `json:"instanceID"`
	EnvID      string `json:"envID"`
	Address    string `json:"address"`
	TraceID    string `json:"traceID"`
	TeamID     string `json:"teamID"`
}

type fc struct {
	pollReady chan struct{}

	ctx context.Context

	cmd *exec.Cmd

	stdout *io.PipeReader
	stderr *io.PipeReader

	metadata *MmdsMetadata

	uffdSocketPath *string

	id string

	socketPath string
	envPath    string

	pid int
}

func (fc *fc) wait() error {
	err := fc.cmd.Wait()
	if err != nil {
		return fmt.Errorf("error waiting for fc process: %w", err)
	}

	return nil
}

func newFirecrackerClient(socketPath string) *client.Firecracker {
	httpClient := client.NewHTTPClient(strfmt.NewFormats())

	transport := firecracker.NewUnixSocketTransport(socketPath, nil, false)
	httpClient.SetTransport(transport)

	return httpClient
}

func (fc *fc) loadSnapshot(
	ctx context.Context,
	tracer trace.Tracer,
	socketPath,
	envPath string,
	metadata interface{},
	uffdSocketPath *string,
	pollReady chan struct{},
) error {
	childCtx, childSpan := tracer.Start(ctx, "load-snapshot", trace.WithAttributes(
		attribute.String("instance.socket.path", socketPath),
		attribute.String("instance.snapshot.root_path", envPath),
	))
	defer childSpan.End()

	httpClient := newFirecrackerClient(socketPath)

	telemetry.ReportEvent(childCtx, "created FC socket client")

	memfilePath := filepath.Join(envPath, MemfileName)
	snapfilePath := filepath.Join(envPath, SnapfileName)

	telemetry.SetAttributes(
		childCtx,
		attribute.String("instance.memfile.path", memfilePath),
		attribute.String("instance.snapfile.path", snapfilePath),
	)

	var backend *models.MemoryBackend

	if uffdSocketPath != nil {
		err := waitForSocket(*uffdSocketPath, socketWaitTimeout)
		if err != nil {
			telemetry.ReportCriticalError(childCtx, err)

			return err
		} else {
			telemetry.ReportEvent(childCtx, "uffd socket ready")
		}

		backendType := models.MemoryBackendBackendTypeUffd
		backend = &models.MemoryBackend{
			BackendPath: uffdSocketPath,
			BackendType: &backendType,
		}
	} else {
		backendType := models.MemoryBackendBackendTypeFile
		backend = &models.MemoryBackend{
			BackendPath: &memfilePath,
			BackendType: &backendType,
		}
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

	_, err := httpClient.Operations.LoadSnapshot(&snapshotConfig)
	if err != nil {
		errMsg := fmt.Errorf("error loading snapshot: %w", err)

		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	if pollReady != nil {
		select {
		case <-pollReady:
			telemetry.ReportEvent(childCtx, "uffd polling ready")

			break
		case <-time.After(uffdPollingTimeout):
			return fmt.Errorf("timeout waiting for the uffd polling to be ready")
		}
	}

	state := models.VMStateResumed
	pauseConfig := operations.PatchVMParams{
		Context: childCtx,
		Body: &models.VM{
			State: &state,
		},
	}

	_, err = httpClient.Operations.PatchVM(&pauseConfig)
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

	_, err = httpClient.Operations.PutMmds(&mmdsConfig)
	if err != nil {
		errMsg := fmt.Errorf("error setting mmds data: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "mmds data set")

	return nil
}

func newFC(
	ctx context.Context,
	tracer trace.Tracer,
	slot IPSlot,
	fsEnv *SandboxFiles,
	mmdsMetadata *MmdsMetadata,
	pollReady chan struct{},
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
		"mount --bind %s %s && ",
		fsEnv.EnvInstancePath,
		fsEnv.BuildDirPath,
	)

	kernelMountCmd := fmt.Sprintf(
		"mount --bind %s %s && ",
		fsEnv.KernelDirPath,
		fsEnv.KernelMountDirPath,
	)

	fcCmd := fmt.Sprintf("%s --api-sock %s", fsEnv.FirecrackerBinaryPath, fsEnv.SocketPath)
	inNetNSCmd := fmt.Sprintf("ip netns exec %s ", slot.NamespaceID())

	telemetry.SetAttributes(childCtx,
		attribute.String("instance.firecracker.command", fcCmd),
		attribute.String("instance.netns.command", inNetNSCmd),
	)

	cmd := exec.Command(
		"unshare",
		"-pfm",
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
		pollReady:      pollReady,
		id:             mmdsMetadata.InstanceID,
		cmd:            cmd,
		stdout:         cmdStdoutReader,
		stderr:         cmdStderrReader,
		ctx:            vmmCtx,
		socketPath:     fsEnv.SocketPath,
		envPath:        fsEnv.EnvPath,
		metadata:       mmdsMetadata,
		uffdSocketPath: fsEnv.UFFDSocketPath,
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
			fmt.Printf("[firecracker stdout]: %s — %s\n", fc.id, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			errMsg := fmt.Errorf("error reading vmm stdout: %w", readerErr)
			telemetry.ReportError(fc.ctx, errMsg)
			fmt.Printf("[firecracker stdout error]: %s — %v\n", fc.id, errMsg)
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
			fmt.Printf("[firecracker stderr]: %s — %v\n", fc.id, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			errMsg := fmt.Errorf("error closing vmm stderr reader: %w", readerErr)
			telemetry.ReportError(fc.ctx, errMsg)
			fmt.Printf("[firecracker stderr error]: %s — %v\n", fc.id, errMsg)
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

	fc.pid = fc.cmd.Process.Pid

	// Wait for the FC process to start so we can use FC API
	err = waitForSocket(fc.socketPath, socketWaitTimeout)
	if err != nil {
		errMsg := fmt.Errorf("error waiting for fc socket: %w", err)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "fc process created socket")

	if loadErr := fc.loadSnapshot(
		childCtx,
		tracer,
		fc.socketPath,
		fc.envPath,
		fc.metadata,
		fc.uffdSocketPath,
		fc.pollReady,
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
		attribute.String("instance.socket.path", fc.socketPath),
		attribute.String("instance.env.id", fc.metadata.EnvID),
		attribute.String("instance.env.path", fc.envPath),
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
