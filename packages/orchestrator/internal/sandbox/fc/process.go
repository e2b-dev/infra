package fc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	txtTemplate "text/template"
	"time"

	gopsutil "github.com/shirou/gopsutil/v4/process"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapio"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/socket"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const startScript = `mount --make-rprivate / &&
mount -t tmpfs tmpfs {{ .buildDir }} -o X-mount.mkdir &&
mount -t tmpfs tmpfs {{ .buildKernelDir }} -o X-mount.mkdir &&
ln -s {{ .rootfsPath }} {{ .buildRootfsPath }} &&
ln -s {{ .kernelPath }} {{ .buildKernelPath }} &&
ip netns exec {{ .namespaceID }} {{ .firecrackerPath }} --api-sock {{ .firecrackerSocket }}`

const (
	waitForFirecrackerExitTimeout = 250 * time.Millisecond
	waitForFirecrackerExitTick    = 25 * time.Millisecond
)

var startScriptTemplate = txtTemplate.Must(txtTemplate.New("fc-start").Parse(startScript))

type ProcessOptions struct {
	// InitScriptPath is the path to the init script that will be executed inside the VM on kernel start.
	InitScriptPath string

	// KernelLogs is a flag to enable kernel logs output to the process stdout.
	KernelLogs bool
	// SystemdToKernelLogs is a flag to enable systemd logs output to the console.
	// It enabled the kernel logs by default too.
	SystemdToKernelLogs bool

	// Stdout is the writer to which the process stdout will be written.
	Stdout io.Writer
	// Stderr is the writer to which the process stderr will be written.
	Stderr io.Writer
}

type Process struct {
	cmd *exec.Cmd

	firecrackerSocketPath string

	slot       *network.Slot
	rootfsPath string
	files      *storage.SandboxFiles

	Exit chan error

	client *apiClient

	buildRootfsPath string
}

func NewProcess(
	ctx context.Context,
	tracer trace.Tracer,
	slot *network.Slot,
	files *storage.SandboxFiles,
	rootfsPath string,
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
		rootfsPath:            rootfsPath,
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
	stdoutExternal io.Writer,
	stderrExternal io.Writer,
) error {
	childCtx, childSpan := tracer.Start(ctx, "configure-fc")
	defer childSpan.End()

	sbxMetadata := sbxlogger.SandboxMetadata{
		SandboxID:  sandboxID,
		TemplateID: templateID,
		TeamID:     teamID,
	}

	stdoutWriter := &zapio.Writer{Log: sbxlogger.I(sbxMetadata).Logger, Level: zap.InfoLevel}
	stdoutWriters := []io.Writer{stdoutWriter}
	if stdoutExternal != nil {
		stdoutWriters = append(stdoutWriters, stdoutExternal)
	}
	p.cmd.Stdout = io.MultiWriter(stdoutWriters...)

	stderrWriter := &zapio.Writer{Log: sbxlogger.I(sbxMetadata).Logger, Level: zap.ErrorLevel}
	stderrWriters := []io.Writer{stderrWriter}
	if stderrExternal != nil {
		stderrWriters = append(stderrWriters, stderrExternal)
	}
	p.cmd.Stderr = io.MultiWriter(stderrWriters...)

	err := utils.SymlinkForce("/dev/null", p.files.SandboxCacheRootfsLinkPath())
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
		defer stderrWriter.Close()
		defer stdoutWriter.Close()

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
	sandboxID string,
	templateID string,
	teamID string,
	vCPUCount int64,
	memoryMB int64,
	hugePages bool,
	options ProcessOptions,
) error {
	childCtx, childSpan := tracer.Start(ctx, "create-fc")
	defer childSpan.End()

	err := p.configure(
		childCtx,
		tracer,
		sandboxID,
		templateID,
		teamID,
		options.Stdout,
		options.Stderr,
	)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error starting fc process: %w", err), fcStopErr)
	}

	// IPv4 configuration - format: [local_ip]::[gateway_ip]:[netmask]:hostname:iface:dhcp_option:[dns]
	ipv4 := fmt.Sprintf("%s::%s:%s:instance:%s:off:%s", p.slot.NamespaceIP(), p.slot.TapIPString(), p.slot.TapMaskString(), p.slot.VpeerName(), p.slot.TapName())
	args := KernelArgs{
		// Disable kernel logs for production to speed the FC operations
		// https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md#logging-and-performance
		"quiet":    "",
		"loglevel": "1",

		// Define kernel init path
		"init": options.InitScriptPath,

		// Networking IPv4 and IPv6
		"ip":            ipv4,
		"ipv6.disable":  "0",
		"ipv6.autoconf": "1",

		// Wait 1 second before exiting FC after panic or reboot
		"panic": "1",

		"reboot":           "k",
		"pci":              "off",
		"i8042.nokbd":      "",
		"i8042.noaux":      "",
		"random.trust_cpu": "on",
	}
	if options.SystemdToKernelLogs {
		args["systemd.journald.forward_to_console"] = ""
	}
	if options.KernelLogs || options.SystemdToKernelLogs {
		// Forward kernel logs to the ttyS0, which will be picked up by the stdout of FC process
		delete(args, "quiet")
		args["console"] = "ttyS0"
		args["loglevel"] = "5" // KERN_NOTICE
	}

	kernelArgs := args.String()
	err = p.client.setBootSource(childCtx, kernelArgs, p.files.BuildKernelPath())
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error setting fc boot source config: %w", err), fcStopErr)
	}
	telemetry.ReportEvent(childCtx, "set fc boot source config")

	// Rootfs
	err = utils.SymlinkForce(p.rootfsPath, p.files.SandboxCacheRootfsLinkPath())
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
		nil,
		nil,
	)
	if err != nil {
		fcStopErr := p.Stop()

		return errors.Join(fmt.Errorf("error starting fc process: %w", err), fcStopErr)
	}

	err = utils.SymlinkForce(p.rootfsPath, p.files.SandboxCacheRootfsLinkPath())
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

// findFirecrackerProcess finds the first "firecracker" process in the process tree, by breadth-first search.
func findFirecrackerProcess(parentPid int32) (*gopsutil.Process, error) {
	proc, err := gopsutil.NewProcess(parentPid)
	if err != nil {
		return nil, err
	}

	queue := []*gopsutil.Process{proc}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		name, _ := current.Name()
		if name == "firecracker" {
			return current, nil
		}

		children, err := current.Children()
		if err == nil {
			queue = append(queue, children...)
		}
	}

	return nil, fmt.Errorf("firecracker not found")
}

// waitForFirecrackerExit waits for the firecracker process to exit.
func waitForFirecrackerExit(fc *gopsutil.Process) error {
	timeout := time.After(waitForFirecrackerExitTimeout)
	// It usually took 50-75ms for the FC to stop being in "running" state after SIGKILL.
	tick := time.Tick(waitForFirecrackerExitTick)

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for firecracker process to exit")
		case <-tick:
			// The FC process should exit and be reaped by the parent unshare process.
			if status, err := fc.IsRunning(); err == nil && !status {
				return nil
			}
		}
	}
}

// killFirecrackerProcess tries to find a firecracker process in the process tree to stop it before we start killing the parent.
func killFirecrackerProcess(unsharePid int) error {
	fc, err := findFirecrackerProcess(int32(unsharePid))
	if err != nil {
		return fmt.Errorf("cannot find firecracker process: %w", err)
	}

	// Send SIGKILL to the firecracker process to make sure it is killed before the unshare process, nbd, and uffd cleanup.
	// Because of our unshare (-p, --kill-child) setup, other signals don't propagate properly.
	err = fc.Kill()
	if err != nil {
		return fmt.Errorf("failed to send KILL to firecracker process: %w", err)
	}

	// We are not calling .Wait on the process as the parent unshare should already reap the child process and calling it twice might result in panic.
	// We are just checking if the process is running and waiting for it to exit.
	err = waitForFirecrackerExit(fc)
	if err != nil {
		return fmt.Errorf("failed to wait for firecracker process to exit: %w", err)
	}

	return nil
}

func (p *Process) Stop() error {
	if p.cmd.Process == nil {
		return fmt.Errorf("fc process not started")
	}

	err := killFirecrackerProcess(p.cmd.Process.Pid)
	if err != nil {
		zap.L().Warn("failed to kill firecracker process before killing unshare process", zap.Error(err), zap.String("sandbox.id", p.files.SandboxID))
	}

	// Send SIGKILL to the unshare process to make sure the parent of the FC process is killed.
	err = p.cmd.Process.Kill()
	if err != nil {
		zap.L().Warn("failed to send KILL to unshare process (parent of the FC process)", zap.Error(err), logger.WithSandboxID(p.files.SandboxID))
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
