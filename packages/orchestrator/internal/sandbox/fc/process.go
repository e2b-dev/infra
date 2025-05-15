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
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/socket"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
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
	cmd *exec.Cmd

	firecrackerSocketPath string

	slot   network.Slot
	rootfs *rootfs.CowDevice
	files  *storage.SandboxFiles

	Exit chan error

	client *apiClient

	buildRootfsPath string
}

func NewProcess(
	ctx context.Context,
	tracer trace.Tracer,
	slot network.Slot,
	files *storage.SandboxFiles,
	rootfs *rootfs.CowDevice,
	baseTemplateID string,
	baseBuildID string,
) (*Process, error) {
	childCtx, childSpan := tracer.Start(ctx, "initialize-fc", trace.WithAttributes(
		attribute.Int("sandbox.slot.index", slot.Idx),
	))
	defer childSpan.End()

	var fcStartScript bytes.Buffer

	baseBuild := storage.NewTemplateFiles(
		baseTemplateID,
		baseBuildID,
		files.KernelVersion,
		files.FirecrackerVersion,
	)

	buildRootfsPath := baseBuild.SandboxRootfsPath()
	err := startScriptTemplate.Execute(&fcStartScript, map[string]interface{}{
		"rootfsPath":        files.SandboxCacheRootfsLinkPath(),
		"kernelPath":        files.CacheKernelPath(),
		"buildDir":          baseBuild.SandboxBuildDir(),
		"buildRootfsPath":   buildRootfsPath,
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

	_, err = os.Stat(files.FirecrackerPath())
	if err != nil {
		return nil, fmt.Errorf("error stating firecracker binary: %w", err)
	}

	_, err = os.Stat(files.CacheKernelPath())
	if err != nil {
		return nil, fmt.Errorf("error stating kernel file: %w", err)
	}

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
		cmd:                   cmd,
		firecrackerSocketPath: files.SandboxFirecrackerSocketPath(),
		client:                newApiClient(files.SandboxFirecrackerSocketPath()),
		rootfs:                rootfs,
		files:                 files,
		slot:                  slot,

		buildRootfsPath: buildRootfsPath,
	}, nil
}

func (p *Process) configure(
	ctx context.Context,
	tracer trace.Tracer,
	sandboxID string,
	templateID string,
	teamID string,
) error {
	childCtx, childSpan := tracer.Start(ctx, "configure-fc")
	defer childSpan.End()

	sbxMetadata := sbxlogger.SandboxMetadata{
		SandboxID:  sandboxID,
		TemplateID: templateID,
		TeamID:     teamID,
	}

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

			sbxlogger.I(sbxMetadata).Info("stdout: "+line, zap.String("sandbox_id", sandboxID))
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			sbxlogger.I(sbxMetadata).Error("error reading fc stdout", zap.Error(readerErr))
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
			sbxlogger.I(sbxMetadata).Error("stderr: "+line, zap.String("sandbox_id", sandboxID))
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			sbxlogger.I(sbxMetadata).Error("error reading fc stderr", zap.Error(readerErr))
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

			zap.L().Error("error waiting for fc process", zap.Error(waitErr))

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

	return nil
}

func (p *Process) Create(
	ctx context.Context,
	tracer trace.Tracer,
	templateID string,
	teamID string,
	vCPUCount int64,
	memoryMB int64,
	hugePages bool,
) error {
	childCtx, childSpan := tracer.Start(ctx, "create-fc")
	defer childSpan.End()

	err := p.configure(
		childCtx,
		tracer,
		"",
		templateID,
		teamID,
	)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error starting fc process: %w", err), fcStopErr)
	}

	// IPv4 configuration - format: [local_ip]::[gateway_ip]:[netmask]:hostname:iface:dhcp_option:[dns]
	ipv4 := fmt.Sprintf("%s::%s:%s:instance:%s:off:%s", p.slot.NamespaceIP(), p.slot.TapIP(), p.slot.TapMaskString(), p.slot.VpeerName(), p.slot.TapName())
	kernelArgs := fmt.Sprintf("quiet loglevel=1 ip=%s ipv6.disable=0 ipv6.autoconf=1 reboot=k panic=1 pci=off nomodules i8042.nokbd i8042.noaux random.trust_cpu=on", ipv4)
	err = p.client.setBootSource(childCtx, kernelArgs, p.files.BuildKernelPath())
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error setting fc boot source config: %w", err), fcStopErr)
	}
	telemetry.ReportEvent(childCtx, "set fc boot source config")

	// Rootfs
	rootfsPath, err := p.rootfs.Path()
	if err != nil {
		return fmt.Errorf("error getting rootfs path: %w", err)
	}
	err = os.Remove(p.files.SandboxCacheRootfsLinkPath())
	if err != nil {
		return fmt.Errorf("error removing rootfs symlink: %w", err)
	}

	err = os.Symlink(rootfsPath, p.files.SandboxCacheRootfsLinkPath())
	if err != nil {
		return fmt.Errorf("error symlinking rootfs: %w", err)
	}

	err = p.client.setRootfsDrive(childCtx, p.buildRootfsPath)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error setting fc drivers config: %w", err), fcStopErr)
	}
	telemetry.ReportEvent(childCtx, "set fc drivers config")

	// Network
	err = p.client.setNetworkInterface(childCtx, p.slot.VpeerName(), p.slot.TapName(), p.slot.TapMAC())
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error setting fc network config: %w", err), fcStopErr)
	}
	telemetry.ReportEvent(childCtx, "set fc network config")

	err = p.client.setMachineConfig(childCtx, vCPUCount, memoryMB, hugePages)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error setting fc machine config: %w", err), fcStopErr)
	}
	telemetry.ReportEvent(childCtx, "set fc machine config")

	err = p.client.startVM(childCtx)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error starting fc: %w", err), fcStopErr)
	}

	telemetry.ReportEvent(childCtx, "started fc")
	return nil
}

func (p *Process) Resume(
	ctx context.Context,
	tracer trace.Tracer,
	mmdsMetadata *MmdsMetadata,
	uffdSocketPath string,
	snapfile template.File,
	uffdReady chan struct{},
) error {
	childCtx, childSpan := tracer.Start(ctx, "resume-fc")
	defer childSpan.End()

	err := p.configure(
		childCtx,
		tracer,
		mmdsMetadata.SandboxId,
		mmdsMetadata.TemplateId,
		mmdsMetadata.TeamId,
	)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error starting fc process: %w", err), fcStopErr)
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
		childCtx,
		uffdSocketPath,
		uffdReady,
		snapfile,
	)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error loading snapshot: %w", err), fcStopErr)
	}

	err = p.client.resumeVM(childCtx)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error resuming vm: %w", err), fcStopErr)
	}

	err = p.client.setMmds(childCtx, mmdsMetadata)
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

// CreateSnapshot VM needs to be paused before creating a snapshot.
func (p *Process) CreateSnapshot(ctx context.Context, tracer trace.Tracer, snapfilePath string, memfilePath string) error {
	ctx, childSpan := tracer.Start(ctx, "create-snapshot-fc")
	defer childSpan.End()

	return p.client.createSnapshot(ctx, snapfilePath, memfilePath)
}
