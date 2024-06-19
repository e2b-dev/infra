package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/go-openapi/strfmt"
	consulapi "github.com/hashicorp/consul/api"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client/operations"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type MmdsMetadata struct {
	InstanceID string `json:"instanceID"`
	EnvID      string `json:"envID"`
	Address    string `json:"address"`
	TraceID    string `json:"traceID"`
	TeamID     string `json:"teamID"`
}

type FC struct {
	ctx context.Context

	cmd    *exec.Cmd
	stdout *io.PipeReader
	stderr *io.PipeReader

	ips *IPSlot

	sbxFiles *SandboxFiles
}

func newFirecrackerClient(socketPath string) *client.Firecracker {
	httpClient := client.NewHTTPClient(strfmt.NewFormats())

	transport := firecracker.NewUnixSocketTransport(socketPath, nil, false)
	httpClient.SetTransport(transport)

	return httpClient
}

func PrepareFC(
	ctx context.Context,
	tracer trace.Tracer,
	consul *consulapi.Client,
	ips *IPSlot,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
) (*FC, error) {
	childCtx, childSpan := tracer.Start(ctx, "prepare-fc")
	defer childSpan.End()

	telemetry.SetAttributes(childCtx, attribute.String("instance.slot.kv.key", ips.KVKey))
	telemetry.ReportEvent(childCtx, "reserved ip slot")

	var err error
	defer func() {
		if err != nil {
			slotErr := ips.Release(childCtx, tracer, consul)
			if slotErr != nil {
				errMsg := fmt.Errorf("error removing network namespace after failed instance start: %w", slotErr)
				telemetry.ReportError(childCtx, errMsg)
			} else {
				telemetry.ReportEvent(childCtx, "released ip slot")
			}
		}
	}()

	err = ips.CreateNetwork(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("failed to create namespaces: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	sbxFiles, err := newSandboxFiles(childCtx, tracer, ips, kernelVersion, firecrackerVersion, hugePages)
	if err != nil {
		errMsg := fmt.Errorf("failed to create sandbox files: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	kernelMountCmd := fmt.Sprintf(
		"mount --bind %s %s && ",
		sbxFiles.KernelDirPath,
		kernelMountDir,
	)

	fcCmd := fmt.Sprintf("%s --api-sock %s", sbxFiles.FirecrackerBinaryPath, sbxFiles.SocketPath)
	inNetNSCmd := fmt.Sprintf("ip netns exec %s ", ips.NamespaceID())

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
		kernelMountCmd+inNetNSCmd+fcCmd,
	)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create a new session
	}

	cmdStdoutReader, cmdStdoutWriter := io.Pipe()
	cmdStderrReader, cmdStderrWriter := io.Pipe()

	cmd.Stderr = cmdStdoutWriter
	cmd.Stdout = cmdStderrWriter

	cmdCtx, _ := tracer.Start(
		trace.ContextWithSpanContext(context.Background(), childSpan.SpanContext()),
		"fc-vmm",
	)

	go func() {
		defer func() {
			readerErr := cmdStdoutReader.Close()
			if readerErr != nil {
				errMsg := fmt.Errorf("error closing vmm stdout reader: %w", readerErr)
				telemetry.ReportError(cmdCtx, errMsg)
			}
		}()

		scanner := bufio.NewScanner(cmdStdoutReader)

		for scanner.Scan() {
			line := scanner.Text()

			telemetry.ReportEvent(cmdCtx, "vmm log",
				attribute.String("type", "stdout"),
				attribute.String("message", line),
			)
			fmt.Printf("[firecracker stdout]: %d — %s\n", ips.SlotIdx, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			errMsg := fmt.Errorf("error reading vmm stdout: %w", readerErr)
			telemetry.ReportError(cmdCtx, errMsg)
			fmt.Printf("[firecracker stdout error]: %d — %v\n", ips.SlotIdx, errMsg)
		} else {
			telemetry.ReportEvent(cmdCtx, "vmm stdout reader closed")
		}
	}()

	go func() {
		defer func() {
			readerErr := cmdStderrReader.Close()
			if readerErr != nil {
				errMsg := fmt.Errorf("error closing vmm stdout reader: %w", readerErr)
				telemetry.ReportError(cmdCtx, errMsg)
			}
		}()

		scanner := bufio.NewScanner(cmdStderrReader)

		for scanner.Scan() {
			line := scanner.Text()

			telemetry.ReportEvent(cmdCtx, "vmm log",
				attribute.String("type", "stderr"),
				attribute.String("message", line),
			)
			fmt.Printf("[firecracker stderr]: %d — %v", ips.SlotIdx, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			errMsg := fmt.Errorf("error closing vmm stderr reader: %w", readerErr)
			telemetry.ReportError(cmdCtx, errMsg)
			fmt.Printf("[firecracker stderr error]: %d — %v", ips.SlotIdx, errMsg)
		} else {
			telemetry.ReportEvent(cmdCtx, "vmm stderr reader closed")
		}
	}()

	err = cmd.Start()
	if err != nil {
		errMsg := fmt.Errorf("error starting fc process: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "started fc process")

	// Wait for the FC process to start so we can use FC API
	err = waitForSocket(sbxFiles.SocketPath, socketWaitTimeout)
	if err != nil {
		errMsg := fmt.Errorf("error waiting for fc socket: %w", err)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "fc process created socket")
	telemetry.SetAttributes(childCtx,
		attribute.String("instance.cmd", cmd.String()),
		attribute.String("instance.cmd.dir", cmd.Dir),
		attribute.String("instance.cmd.path", cmd.Path),
	)

	vmmCtx, _ := tracer.Start(
		trace.ContextWithSpanContext(context.Background(), childSpan.SpanContext()),
		"fc-vmm",
	)

	return &FC{
		ctx: vmmCtx,

		cmd:    cmd,
		stdout: cmdStdoutReader,
		stderr: cmdStderrReader,

		ips: ips,

		sbxFiles: sbxFiles,
	}, nil
}

func (fc *FC) loadSnapshot(
	ctx context.Context,
	tracer trace.Tracer,
	envPath string,
	metadata interface{},
	uffdSocketPath *string,
) error {
	childCtx, childSpan := tracer.Start(ctx, "load-snapshot", trace.WithAttributes(
		attribute.String("instance.socket.path", fc.sbxFiles.SocketPath),
		attribute.String("instance.snapshot.root_path", envPath),
	))
	defer childSpan.End()

	httpClient := newFirecrackerClient(fc.sbxFiles.SocketPath)
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
			ResumeVM:            true,
			EnableDiffSnapshots: false,
			MemBackend:          backend,
			SnapshotPath:        &snapfilePath,
		},
	}

	mmdsConfig := operations.PutMmdsParams{
		Context: childCtx,
		Body:    metadata,
	}

	_, err := httpClient.Operations.PutMmds(&mmdsConfig)
	if err != nil {
		telemetry.ReportCriticalError(childCtx, err)
		return err
	}

	telemetry.ReportEvent(childCtx, "mmds data set")

	_, err = httpClient.Operations.LoadSnapshot(&snapshotConfig)
	if err != nil {
		telemetry.ReportCriticalError(childCtx, err)
		return err
	}
	telemetry.ReportEvent(childCtx, "snapshot loaded")

	return nil
}

func (fc *FC) stop(ctx context.Context, tracer trace.Tracer) {
	childCtx, childSpan := tracer.Start(ctx, "stop-fc", trace.WithAttributes(
		attribute.String("instance.cmd", fc.cmd.String()),
		attribute.String("instance.cmd.dir", fc.cmd.Dir),
		attribute.String("instance.cmd.path", fc.cmd.Path),
	))
	defer childSpan.End()

	err := fc.cmd.Process.Kill()
	if err != nil {
		errMsg := fmt.Errorf("failed to send KILL to FC process: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "sent KILL to FC process")
	}

	return
}
