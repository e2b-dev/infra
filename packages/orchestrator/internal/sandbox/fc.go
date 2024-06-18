package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"github.com/KarpelesLab/reflink"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/go-openapi/strfmt"
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

	mu sync.Mutex

	cmd *exec.Cmd

	stdout *io.PipeReader
	stderr *io.PipeReader

	metadata *MmdsMetadata

	uffd           *uffd
	fsEnv          *SandboxFiles
	uffdSocketPath string
	httpClient     *client.Firecracker

	id string

	socketPath string
	envPath    string

	ips *IPSlot
	pid int
}

func (fc *FC) wait() error {
	return fc.cmd.Wait()
}

func newFirecrackerClient(socketPath string) *client.Firecracker {
	httpClient := client.NewHTTPClient(strfmt.NewFormats())

	transport := firecracker.NewUnixSocketTransport(socketPath, nil, false)
	httpClient.SetTransport(transport)

	return httpClient
}

func (fc *FC) loadSnapshot(
	ctx context.Context,
	tracer trace.Tracer,
	httpClient *client.Firecracker,
	socketPath,
	envPath string,
	metadata interface{},
	uffdSocketPath string,
) error {
	childCtx, childSpan := tracer.Start(ctx, "load-snapshot", trace.WithAttributes(
		attribute.String("instance.socket.path", socketPath),
		attribute.String("instance.snapshot.root_path", envPath),
	))
	defer childSpan.End()

	telemetry.ReportEvent(childCtx, "created FC socket client")

	memfilePath := filepath.Join(envPath, MemfileName)
	snapfilePath := filepath.Join(envPath, SnapfileName)

	telemetry.SetAttributes(
		childCtx,
		attribute.String("instance.memfile.path", memfilePath),
		attribute.String("instance.snapfile.path", snapfilePath),
	)

	err := waitForSocket(uffdSocketPath, socketWaitTimeout)
	if err != nil {
		telemetry.ReportCriticalError(childCtx, err)

		return err
	} else {
		telemetry.ReportEvent(childCtx, "uffd socket ready")
	}

	backendType := models.MemoryBackendBackendTypeUffd
	backend := &models.MemoryBackend{
		BackendPath: &uffdSocketPath,
		BackendType: &backendType,
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

	_, err = httpClient.Operations.PutMmds(&mmdsConfig)
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

func newFC(
	ctx context.Context,
	tracer trace.Tracer,
	slot *IPSlot,
	fsEnv *SandboxFiles,
) *FC {
	childCtx, childSpan := tracer.Start(ctx, "initialize-fc", trace.WithAttributes(
		attribute.Int("instance.slot.index", slot.SlotIdx),
	))
	defer childSpan.End()

	vmmCtx, _ := tracer.Start(
		trace.ContextWithSpanContext(context.Background(), childSpan.SpanContext()),
		"fc-vmm",
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
		kernelMountCmd+inNetNSCmd+fcCmd,
	)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create a new session
	}

	cmdStdoutReader, cmdStdoutWriter := io.Pipe()
	cmdStderrReader, cmdStderrWriter := io.Pipe()

	cmd.Stderr = cmdStdoutWriter
	cmd.Stdout = cmdStderrWriter

	return &FC{
		cmd:            cmd,
		stdout:         cmdStdoutReader,
		stderr:         cmdStderrReader,
		ctx:            vmmCtx,
		ips:            slot,
		socketPath:     fsEnv.SocketPath,
		uffdSocketPath: fsEnv.UFFDSocketPath,
	}
}

func (fc *FC) prep(
	ctx context.Context,
	tracer trace.Tracer,
) error {
	childCtx, childSpan := tracer.Start(ctx, "start-fc")
	defer childSpan.End()

	httpClient := newFirecrackerClient(fc.socketPath)
	fc.httpClient = httpClient

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

	fc.pid = fc.cmd.Process.Pid

	defer func() {
		if err != nil {
			fc.stop(childCtx, tracer)
		}
	}()

	telemetry.ReportEvent(childCtx, "started fc process")

	// Wait for the FC process to start so we can use FC API
	err = waitForSocket(fc.socketPath, socketWaitTimeout)
	if err != nil {
		errMsg := fmt.Errorf("error waiting for fc socket: %w", err)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "fc process created socket")

	return nil
}

func (fc *FC) start(ctx context.Context, tracer trace.Tracer, slotIdx int, envID string) error {
	childCtx, childSpan := tracer.Start(ctx, "start-fc")
	defer childSpan.End()

	envPath := filepath.Join(envsDisk, envID)
	envInstancePath := filepath.Join(envPath, EnvInstancesDirName, strconv.Itoa(slotIdx))

	// Mount overlay
	buildIDPath := filepath.Join(envPath, BuildIDName)

	err := os.MkdirAll(envInstancePath, 0o777)
	if err != nil {
		telemetry.ReportError(ctx, err)
	}

	data, err := os.ReadFile(buildIDPath)
	if err != nil {
		return fmt.Errorf("failed reading build id for the env %s: %w", envID, err)
	}

	buildID := string(data)
	buildDirPath := filepath.Join(envPath, BuildDirName, buildID)

	mkdirErr := os.MkdirAll(buildDirPath, 0o777)
	if mkdirErr != nil {
		telemetry.ReportError(ctx, err)
	}

	err = reflink.Always(
		filepath.Join(envPath, RootfsName),
		filepath.Join(envInstancePath, RootfsName),
	)
	if err != nil {
		errMsg := fmt.Errorf("error creating reflinked rootfs: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return errMsg
	}

	rootfsMountCmd := fmt.Sprintf(
		"mount --bind %s %s",
		envInstancePath,
		buildDirPath,
	)

	cmd := exec.Command(
		"nsenter", "--mount=/proc/"+strconv.Itoa(fc.pid)+"/ns/mnt", "--",
		"bash",
		"-c",
		rootfsMountCmd,
	)

	err = cmd.Start()
	if err != nil {
		errMsg := fmt.Errorf("error starting fc process: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
		fmt.Printf("error starting fc process: %v\n", err)

		return errMsg
	}

	cmdStdoutReader, cmdStdoutWriter := io.Pipe()
	cmdStderrReader, cmdStderrWriter := io.Pipe()

	cmd.Stderr = cmdStdoutWriter
	cmd.Stdout = cmdStderrWriter

	go func() {
		defer func() {
			readerErr := cmdStdoutReader.Close()
			if readerErr != nil {
				errMsg := fmt.Errorf("error closing vmm stdout reader: %w", readerErr)
				telemetry.ReportError(fc.ctx, errMsg)
			}
		}()

		scanner := bufio.NewScanner(cmdStdoutReader)

		for scanner.Scan() {
			line := scanner.Text()

			telemetry.ReportEvent(fc.ctx, "vmm log",
				attribute.String("type", "stdout"),
				attribute.String("message", line),
			)
			fmt.Printf("[XXX stdout]: %s — %s\n", fc.id, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			errMsg := fmt.Errorf("error reading vmm stdout: %w", readerErr)
			telemetry.ReportError(fc.ctx, errMsg)
			fmt.Printf("[XXX stdout error]: %s — %v\n", fc.id, errMsg)
		} else {
			telemetry.ReportEvent(fc.ctx, "vmm stdout reader closed")
		}
	}()

	go func() {
		defer func() {
			readerErr := cmdStderrReader.Close()
			if readerErr != nil {
				errMsg := fmt.Errorf("error closing vmm stdout reader: %w", readerErr)
				telemetry.ReportError(fc.ctx, errMsg)
			}
		}()

		scanner := bufio.NewScanner(cmdStderrReader)

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

	if err := fc.loadSnapshot(
		childCtx,
		tracer,
		fc.httpClient,
		fc.socketPath,
		fc.envPath,
		fc.metadata,
		fc.uffdSocketPath,
	); err != nil {
		fc.stop(childCtx, tracer)

		errMsg := fmt.Errorf("failed to load snapshot: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "loaded snapshot")

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
