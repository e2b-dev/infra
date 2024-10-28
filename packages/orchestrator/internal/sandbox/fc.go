package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"text/template"

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

	cmd *exec.Cmd

	stdout *io.PipeReader
	stderr *io.PipeReader

	metadata *MmdsMetadata

	uffdSocketPath        string
	firecrackerSocketPath string
	rootfsPath            string

	fcExit chan error
}

func (fc *fc) wait() error {
	// TODO: The wait is not called right after the FC process because we are first starting the uffd.
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
	rootfsPath string,
) error {
	childCtx, childSpan := tracer.Start(ctx, "load-snapshot", trace.WithAttributes(
		attribute.String("sandbox.socket.path", firecrackerSocketPath),
	))
	defer childSpan.End()

	client := client.NewHTTPClient(strfmt.NewFormats())
	transport := firecracker.NewUnixSocketTransport(firecrackerSocketPath, nil, false)
	client.SetTransport(transport)

	var backend *models.MemoryBackend

	err := waitForSocket(childCtx, uffdSocketPath)
	if err != nil {
		return fmt.Errorf("error waiting for uffd socket: %w", err)
	}

	snapfilePath, err := snapfile.GetPath()
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
		return fmt.Errorf("error loading snapshot: %w", err)
	}

	rootfs := "rootfs"
	pathOnHost := rootfsPath
	driversConfig := operations.PatchGuestDriveByIDParams{
		Context: childCtx,
		DriveID: rootfs,
		Body: &models.PartialDrive{
			DriveID:    &rootfs,
			PathOnHost: pathOnHost,
		},
	}

	_, err = client.Operations.PatchGuestDriveByID(&driversConfig)
	if err != nil {
		errMsg := fmt.Errorf("error setting fc drivers config: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	select {
	case <-childCtx.Done():
		return fmt.Errorf("context canceled while waiting for uffd ready: %w", childCtx.Err())
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
		return fmt.Errorf("error resuming vm: %w", err)
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

touch {{ .buildRootfsPath }} &&

ip netns exec {{ .namespaceID }} {{ .firecrackerPath }} --api-sock {{ .firecrackerSocket }}`

var fcStartScriptTemplate = template.Must(template.New("fc-start").Parse(fcStartScript))

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

	rootfsPath, err := rootfs.NbdPath(childCtx)
	if err != nil {
		return nil, fmt.Errorf("error getting rootfs path: %w", err)
	}

	var fcStartScript bytes.Buffer

	err = fcStartScriptTemplate.Execute(&fcStartScript, map[string]interface{}{
		"rootfsPath":        rootfsPath,
		"buildDir":          files.BuildDir(),
		"buildRootfsPath":   files.BuildRootfsPath(),
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

	return &fc{
		fcExit:                make(chan error, 1),
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

func (fc *fc) start(
	ctx context.Context,
	tracer trace.Tracer,
) error {
	childCtx, childSpan := tracer.Start(ctx, "start-fc")
	defer childSpan.End()

	childCtx, cancelFcCmd := context.WithCancel(childCtx)
	defer cancelFcCmd()

	go func() {
		defer func() {
			readerErr := fc.stdout.Close()
			if readerErr != nil {
				fmt.Fprintf(os.Stderr, "[sandbox %s]: error closing fc stdout reader: %v\n", fc.metadata.SandboxId, readerErr)
			}
		}()

		scanner := bufio.NewScanner(fc.stdout)

		for scanner.Scan() {
			line := scanner.Text()

			fmt.Fprintf(os.Stdout, "[sandbox %s]: stdout: %s\n", fc.metadata.SandboxId, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			fmt.Fprintf(os.Stderr, "[sandbox %s]: error reading fc stdout: %v\n", fc.metadata.SandboxId, readerErr)
		}
	}()

	go func() {
		defer func() {
			readerErr := fc.stderr.Close()
			if readerErr != nil {
				fmt.Fprintf(os.Stderr, "[sandbox %s]: error closing fc stderr reader: %v\n", fc.metadata.SandboxId, readerErr)
			}
		}()

		scanner := bufio.NewScanner(fc.stderr)

		for scanner.Scan() {
			line := scanner.Text()

			fmt.Fprintf(os.Stderr, "[sandbox %s]: stderr: %s\n", fc.metadata.SandboxId, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			fmt.Fprintf(os.Stderr, "[sandbox %s]: error reading fc stderr: %v\n", fc.metadata.SandboxId, readerErr)
		}
	}()

	err := fc.cmd.Start()
	if err != nil {
		return fmt.Errorf("error starting fc process: %w", err)
	}

	go func() {
		fmt.Printf("[sandbox %s]: waiting for fc process\n", fc.metadata.SandboxId)
		fc.fcExit <- fc.wait()
		fmt.Printf("[sandbox %s]: fc process exited\n", fc.metadata.SandboxId)
		close(fc.fcExit)
		cancelFcCmd()
	}()

	defer func() {
		if err != nil {
			fcErr := fc.stop()
			if fcErr != nil {
				telemetry.ReportError(childCtx, fmt.Errorf("failed to stop FC: %w", fcErr))
			}
		}
	}()

	// Wait for the FC process to start so we can use FC API
	err = waitForSocket(childCtx, fc.firecrackerSocketPath)
	if err != nil {
		return fmt.Errorf("error waiting for fc socket: %w", err)
	}

	err = fc.loadSnapshot(
		childCtx,
		tracer,
		fc.firecrackerSocketPath,
		fc.uffdSocketPath,
		fc.metadata,
		fc.snapfile,
		fc.uffdReady,
		fc.rootfsPath,
	)
	if err != nil {
		return fmt.Errorf("failed to load snapshot: %w", err)
	}

	telemetry.SetAttributes(
		childCtx,
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
