//go:build linux
// +build linux

package build

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/go-openapi/strfmt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client/operations"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build/writer"
)

const (
	fcMacAddress = "02:FC:00:00:00:05"

	// IPv4 configuration
	fcAddr     = "169.254.0.21"
	fcMaskLong = "255.255.255.252"
	fcDNS      = "8.8.8.8" // Google DNS

	fcIfaceID  = "eth0"
	tmpDirPath = "/tmp"

	socketReadyCheckInterval = 100 * time.Millisecond
	socketWaitTimeout        = 2 * time.Second

	waitTimeForFCConfig = 500 * time.Millisecond

	waitTimeForFCStart  = 10 * time.Second
	waitTimeForStartCmd = 15 * time.Second
)

type Snapshot struct {
	fc     *exec.Cmd
	client *client.Firecracker

	env        *Env
	socketPath string
}

func waitForSocket(socketPath string, timeout time.Duration) error {
	start := time.Now()

	for {
		_, err := os.Stat(socketPath)
		if err == nil {
			// Socket file exists
			return nil
		} else if os.IsNotExist(err) {
			// Socket file doesn't exist yet

			// Check if timeout has been reached
			elapsed := time.Since(start)
			if elapsed >= timeout {
				return fmt.Errorf("timeout reached while waiting for socket file")
			}

			// Wait for a short duration before checking again
			time.Sleep(socketReadyCheckInterval)
		} else {
			// Error occurred while checking for socket file
			return err
		}
	}
}

func newFirecrackerClient(socketPath string) *client.Firecracker {
	httpClient := client.NewHTTPClient(strfmt.NewFormats())

	transport := firecracker.NewUnixSocketTransport(socketPath, nil, false)
	httpClient.SetTransport(transport)

	return httpClient
}

func NewSnapshot(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor, env *Env, network *FCNetwork, rootfs *Rootfs) (*Snapshot, error) {
	childCtx, childSpan := tracer.Start(ctx, "new-snapshot")
	defer childSpan.End()

	postProcessor.WriteMsg("Creating snapshot")

	socketFileName := fmt.Sprintf("fc-sock-%s.sock", env.BuildId)
	socketPath := filepath.Join(tmpDirPath, socketFileName)

	fcClient := newFirecrackerClient(socketPath)

	telemetry.ReportEvent(childCtx, "created fc client")

	snapshot := &Snapshot{
		socketPath: socketPath,
		client:     fcClient,
		env:        env,
		fc:         nil,
	}

	defer snapshot.cleanupFC(childCtx, tracer)

	err := snapshot.startFCProcess(
		childCtx,
		tracer,
		env.FirecrackerPath(),
		network.namespaceID,
		storage.KernelMountDir,
		env.CacheKernelDir(),
	)
	if err != nil {
		errMsg := fmt.Errorf("error starting fc process: %w", err)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "started fc process")

	err = snapshot.configureFC(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("error configuring fc: %w", err)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "configured fc")

	postProcessor.WriteMsg("Waiting for VM to start...")
	// Wait for all necessary things in FC to start
	// TODO: Maybe init should signalize when it's ready?
	time.Sleep(waitTimeForFCStart)
	telemetry.ReportEvent(childCtx, "waited for fc to start", attribute.Float64("seconds", float64(waitTimeForFCStart/time.Second)))
	postProcessor.WriteMsg("VM started")

	if env.StartCmd != "" {
		postProcessor.WriteMsg("Waiting for start command to be healthy...")
		// HACK: This is a temporary fix for a customer that needs a bigger time to start the command.
		// TODO: Remove this after we can add customizable wait time for building templates.
		if env.TemplateId == "zegbt9dl3l2ixqem82mm" || env.TemplateId == "ot5bidkk3j2so2j02uuz" || env.TemplateId == "0zeou1s7agaytqitvmzc" {
			time.Sleep(120 * time.Second)
		} else {
			time.Sleep(waitTimeForStartCmd)
		}
		postProcessor.WriteMsg("Waiting for start command is healthy")
		telemetry.ReportEvent(childCtx, "waited for start command", attribute.Float64("seconds", float64(waitTimeForStartCmd/time.Second)))
	}

	err = snapshot.pauseFC(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("error pausing fc: %w", err)

		return nil, errMsg
	}

	err = snapshot.snapshotFC(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("error snapshotting fc: %w", err)

		return nil, errMsg
	}

	return snapshot, nil
}

func (s *Snapshot) startFCProcess(
	ctx context.Context,
	tracer trace.Tracer,
	fcBinaryPath,
	networkNamespaceID,
	kernelMountDir,
	kernelDirPath string,
) error {
	childCtx, childSpan := tracer.Start(ctx, "start-fc-process")
	defer childSpan.End()

	kernelMountCmd := fmt.Sprintf(
		"mount --bind %s %s && ",
		kernelDirPath,
		kernelMountDir,
	)

	inNetNSCmd := fmt.Sprintf("ip netns exec %s ", networkNamespaceID)
	fcCmd := fmt.Sprintf("%s --api-sock %s", fcBinaryPath, s.socketPath)

	s.fc = exec.CommandContext(childCtx, "unshare", "-pm", "--kill-child", "--", "bash", "-c", kernelMountCmd+inNetNSCmd+fcCmd)

	fcVMStdoutWriter := telemetry.NewEventWriter(childCtx, "stdout")
	fcVMStderrWriter := telemetry.NewEventWriter(childCtx, "stderr")

	stdoutPipe, err := s.fc.StdoutPipe()
	if err != nil {
		errMsg := fmt.Errorf("error creating fc stdout pipe: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	stderrPipe, err := s.fc.StderrPipe()
	if err != nil {
		errMsg := fmt.Errorf("error creating fc stderr pipe: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		closeErr := stdoutPipe.Close()
		if closeErr != nil {
			closeErrMsg := fmt.Errorf("error closing fc stdout pipe: %w", closeErr)
			telemetry.ReportError(childCtx, closeErrMsg)
		}

		return errMsg
	}

	var outputWaitGroup sync.WaitGroup

	outputWaitGroup.Add(1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)

		for scanner.Scan() {
			line := scanner.Text()
			fcVMStdoutWriter.Write([]byte(line))
		}

		outputWaitGroup.Done()
	}()

	outputWaitGroup.Add(1)
	go func() {
		scanner := bufio.NewScanner(stderrPipe)

		for scanner.Scan() {
			line := scanner.Text()
			fcVMStderrWriter.Write([]byte(line))
		}

		outputWaitGroup.Done()
	}()

	err = s.fc.Start()
	if err != nil {
		errMsg := fmt.Errorf("error starting fc process: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "started fc process")

	go func() {
		anonymousChildCtx, anonymousChildSpan := tracer.Start(ctx, "handle-fc-process-wait")
		defer anonymousChildSpan.End()

		outputWaitGroup.Wait()

		waitErr := s.fc.Wait()
		if err != nil {
			errMsg := fmt.Errorf("error waiting for fc process: %w", waitErr)
			telemetry.ReportError(anonymousChildCtx, errMsg)
		} else {
			telemetry.ReportEvent(anonymousChildCtx, "fc process exited")
		}
	}()

	// Wait for the FC process to start so we can use FC API
	err = waitForSocket(s.socketPath, socketWaitTimeout)
	if err != nil {
		errMsg := fmt.Errorf("error waiting for fc socket: %w", err)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "fc process created socket")

	return nil
}

func (s *Snapshot) configureFC(ctx context.Context, tracer trace.Tracer) error {
	childCtx, childSpan := tracer.Start(ctx, "configure-fc")
	defer childSpan.End()

	// IPv4 configuration - format: [local_ip]::[gateway_ip]:[netmask]:hostname:iface:dhcp_option:[dns]
	ipv4 := fmt.Sprintf("%s::%s:%s:instance:%s:off:%s", fcAddr, fcTapAddress, fcMaskLong, fcIfaceID, fcDNS)
	kernelArgs := fmt.Sprintf("quiet loglevel=1 ip=%s ipv6.disable=0 ipv6.autoconf=1 reboot=k panic=1 pci=off nomodules i8042.nokbd i8042.noaux random.trust_cpu=on", ipv4)
	kernelImagePath := storage.KernelMountedPath
	bootSourceConfig := operations.PutGuestBootSourceParams{
		Context: childCtx,
		Body: &models.BootSource{
			BootArgs:        kernelArgs,
			KernelImagePath: &kernelImagePath,
		},
	}

	_, err := s.client.Operations.PutGuestBootSource(&bootSourceConfig)
	if err != nil {
		errMsg := fmt.Errorf("error setting fc boot source config: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "set fc boot source config")

	rootfs := "rootfs"
	ioEngine := "Async"
	isRootDevice := true
	isReadOnly := false
	pathOnHost := s.env.BuildRootfsPath()
	driversConfig := operations.PutGuestDriveByIDParams{
		Context: childCtx,
		DriveID: rootfs,
		Body: &models.Drive{
			DriveID:      &rootfs,
			PathOnHost:   pathOnHost,
			IsRootDevice: &isRootDevice,
			IsReadOnly:   isReadOnly,
			IoEngine:     &ioEngine,
		},
	}

	_, err = s.client.Operations.PutGuestDriveByID(&driversConfig)
	if err != nil {
		errMsg := fmt.Errorf("error setting fc drivers config: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "set fc drivers config")

	ifaceID := fcIfaceID
	hostDevName := fcTapName
	networkConfig := operations.PutGuestNetworkInterfaceByIDParams{
		Context: childCtx,
		IfaceID: ifaceID,
		Body: &models.NetworkInterface{
			IfaceID:     &ifaceID,
			GuestMac:    fcMacAddress,
			HostDevName: &hostDevName,
		},
	}

	_, err = s.client.Operations.PutGuestNetworkInterfaceByID(&networkConfig)
	if err != nil {
		errMsg := fmt.Errorf("error setting fc network config: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "set fc network config")

	smt := true
	trackDirtyPages := false

	machineConfig := &models.MachineConfiguration{
		VcpuCount:       &s.env.VCpuCount,
		MemSizeMib:      &s.env.MemoryMB,
		Smt:             &smt,
		TrackDirtyPages: &trackDirtyPages,
	}

	if s.env.Hugepages() {
		machineConfig.HugePages = models.MachineConfigurationHugePagesNr2M
	}

	machineConfigParams := operations.PutMachineConfigurationParams{
		Context: childCtx,
		Body:    machineConfig,
	}

	// hack for 16GB RAM templates
	// todo fixme
	// robert's (r33drichards) test template 3df60qm8cuefu2pub3mm
	// customer template id raocbwn4f2mtdrjuajsx
	if s.env.TemplateId == "3df60qm8cuefu2pub3mm" || s.env.TemplateId == "raocbwn4f2mtdrjuajsx" {
		var sixteenGBRam int64 = 16384
		machineConfig.MemSizeMib = &sixteenGBRam
	}

	_, err = s.client.Operations.PutMachineConfiguration(&machineConfigParams)
	if err != nil {
		errMsg := fmt.Errorf("error setting fc machine config: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "set fc machine config")

	mmdsVersion := "V2"
	mmdsConfig := operations.PutMmdsConfigParams{
		Context: childCtx,
		Body: &models.MmdsConfig{
			Version:           &mmdsVersion,
			NetworkInterfaces: []string{fcIfaceID},
		},
	}

	_, err = s.client.Operations.PutMmdsConfig(&mmdsConfig)
	if err != nil {
		errMsg := fmt.Errorf("error setting fc mmds config: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "set fc mmds config")

	// We may need to sleep before start - previous configuration is processes asynchronously. How to do this sync or in one go?
	time.Sleep(waitTimeForFCConfig)

	start := models.InstanceActionInfoActionTypeInstanceStart
	startActionParams := operations.CreateSyncActionParams{
		Context: childCtx,
		Info: &models.InstanceActionInfo{
			ActionType: &start,
		},
	}

	_, err = s.client.Operations.CreateSyncAction(&startActionParams)
	if err != nil {
		errMsg := fmt.Errorf("error starting fc: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "started fc")

	return nil
}

func (s *Snapshot) pauseFC(ctx context.Context, tracer trace.Tracer) error {
	childCtx, childSpan := tracer.Start(ctx, "pause-fc")
	defer childSpan.End()

	state := models.VMStatePaused
	pauseConfig := operations.PatchVMParams{
		Context: childCtx,
		Body: &models.VM{
			State: &state,
		},
	}

	_, err := s.client.Operations.PatchVM(&pauseConfig)
	if err != nil {
		errMsg := fmt.Errorf("error pausing vm: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "paused fc")

	return nil
}

func (s *Snapshot) snapshotFC(ctx context.Context, tracer trace.Tracer) error {
	childCtx, childSpan := tracer.Start(ctx, "snapshot-fc")
	defer childSpan.End()

	memfilePath := s.env.BuildMemfilePath()
	snapfilePath := s.env.BuildSnapfilePath()
	snapshotConfig := operations.CreateSnapshotParams{
		Context: childCtx,
		Body: &models.SnapshotCreateParams{
			SnapshotType: models.SnapshotCreateParamsSnapshotTypeFull,
			MemFilePath:  &memfilePath,
			SnapshotPath: &snapfilePath,
		},
	}

	_, err := s.client.Operations.CreateSnapshot(&snapshotConfig)
	if err != nil {
		errMsg := fmt.Errorf("error creating vm snapshot: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "created vm snapshot")

	return nil
}

func (s *Snapshot) cleanupFC(ctx context.Context, tracer trace.Tracer) {
	childCtx, childSpan := tracer.Start(ctx, "cleanup-fc")
	defer childSpan.End()

	if s.fc != nil {
		err := s.fc.Cancel()
		if err != nil {
			errMsg := fmt.Errorf("error killing fc process: %w", err)
			telemetry.ReportError(childCtx, errMsg)
		} else {
			telemetry.ReportEvent(childCtx, "killed fc process")
		}
	}

	err := os.RemoveAll(s.socketPath)
	if err != nil {
		errMsg := fmt.Errorf("error removing fc socket %w", err)
		telemetry.ReportError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "removed fc socket")
	}
}
