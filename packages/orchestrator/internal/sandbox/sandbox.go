package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/schema"
	"go.opentelemetry.io/otel/attribute"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	consul "github.com/hashicorp/consul/api"
)

const (
	waitForUffd       = 80 * time.Millisecond
	uffdCheckInterval = 10 * time.Millisecond
)

var logsProxyAddress = os.Getenv("LOGS_PROXY_ADDRESS")

var httpClient = http.Client{
	Timeout: 5 * time.Second,
}

type Sandbox struct {
	slot     *IPSlot
	sbxFiles *SandboxFiles
	envFiles *EnvFiles

	fc   *FC
	uffd *uffd

	Sandbox   *orchestrator.SandboxConfig
	StartedAt time.Time
	TraceID   string
}

func NewSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	consul *consul.Client,
	dns *DNS,
	fcPool *pool.Pool[*FC],
	networkPool *pool.Pool[*IPSlot],
	config *orchestrator.SandboxConfig,
	traceID string,
) (*Sandbox, error) {
	childCtx, childSpan := tracer.Start(ctx, "new-sandbox", trace.WithAttributes(
		attribute.String("instance.id", config.SandboxID),
	))
	defer childSpan.End()

	var preFC *FC
	if config.FirecrackerVersion == schema.DefaultFirecrackerVersion && config.KernelVersion == schema.DefaultKernelVersion {
		preFC = fcPool.Get()
		//	TODO: cleanup preFC
	} else {
		//	TODO:
	}

	telemetry.SetAttributes(childCtx, attribute.Int("instance.slot.index", preFC.ips.SlotIdx))
	telemetry.ReportEvent(childCtx, "created network")

	envFiles, err := newEnvFiles(
		childCtx,
		tracer,
		config.TemplateID,
		config.SandboxID,
	)
	if err != nil {
		errMsg := fmt.Errorf("failed to assemble env files info for FC: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "assembled env files info")

	err = envFiles.Ensure(childCtx)
	if err != nil {
		errMsg := fmt.Errorf("failed to create env for FC: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "created env for FC")

	defer func() {
		if err != nil {
			envErr := envFiles.Cleanup(childCtx, tracer)
			if envErr != nil {
				errMsg := fmt.Errorf("error deleting env after failed fc start: %w", err)
				telemetry.ReportCriticalError(childCtx, errMsg)
			} else {
				telemetry.ReportEvent(childCtx, "deleted env")
			}
		}
	}()

	var uffd *uffd
	if preFC.sbxFiles.UFFDSocketPath != nil {
		uffd = preFC.newUFFD(envFiles.EnvPath)

		uffdErr := uffd.start()
		if err != nil {
			errMsg := fmt.Errorf("failed to start uffd: %w", uffdErr)
			telemetry.ReportCriticalError(childCtx, errMsg)

			return nil, errMsg
		}

		// Wait for uffd to initialize — it should be possible to handle this better?
	uffdWait:
		for {
			select {
			case <-time.After(waitForUffd):
				fmt.Printf("waiting for uffd to initialize")
				return nil, fmt.Errorf("timeout waiting to uffd to initialize")
			case <-childCtx.Done():
				return nil, childCtx.Err()
			default:
				isRunning, _ := checkIsRunning(uffd.cmd.Process)
				fmt.Printf("uffd is running: %v\n", isRunning)
				if isRunning {
					break uffdWait
				}

				time.Sleep(uffdCheckInterval)
			}
		}
	}

	// Improve logs
	rootfsMountCmd := fmt.Sprintf(
		"mount --bind %s %s",
		envFiles.EnvInstancePath,
		envFiles.BuildDirPath,
	)

	cmd := exec.Command(
		"nsenter", "--target", strconv.Itoa(preFC.cmd.Process.Pid), "--",
		"bash",
		"-c",
		rootfsMountCmd,
	)

	err = cmd.Start()
	if err != nil {
		errMsg := fmt.Errorf("error starting fc process: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
		fmt.Printf("error starting fc process: %v\n", err)

		return nil, errMsg
	}

	cmdStdoutReader, cmdStdoutWriter := io.Pipe()
	cmdStderrReader, cmdStderrWriter := io.Pipe()

	cmd.Stderr = cmdStdoutWriter
	cmd.Stdout = cmdStderrWriter

	err = cmd.Wait()
	if err != nil {
		errMsg := fmt.Errorf("error waiting for fc process: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
		fmt.Printf("error waiting for fc process: %v\n", err)

		return nil, errMsg
	}

	go func() {
		defer func() {
			readerErr := cmdStdoutReader.Close()
			if readerErr != nil {
				errMsg := fmt.Errorf("error closing vmm stdout reader: %w", readerErr)
				telemetry.ReportError(preFC.ctx, errMsg)
			}
		}()

		scanner := bufio.NewScanner(cmdStdoutReader)

		for scanner.Scan() {
			line := scanner.Text()

			telemetry.ReportEvent(preFC.ctx, "vmm log",
				attribute.String("type", "stdout"),
				attribute.String("message", line),
			)
			fmt.Printf("[XXX stdout]: %s — %s\n", preFC.ips.SlotIdx, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			errMsg := fmt.Errorf("error reading vmm stdout: %w", readerErr)
			telemetry.ReportError(preFC.ctx, errMsg)
			fmt.Printf("[XXX stdout error]: %s — %v\n", preFC.ips.SlotIdx, errMsg)
		} else {
			telemetry.ReportEvent(preFC.ctx, "vmm stdout reader closed")
		}
	}()

	go func() {
		defer func() {
			readerErr := cmdStderrReader.Close()
			if readerErr != nil {
				errMsg := fmt.Errorf("error closing vmm stdout reader: %w", readerErr)
				telemetry.ReportError(preFC.ctx, errMsg)
			}
		}()

		scanner := bufio.NewScanner(cmdStderrReader)

		for scanner.Scan() {
			line := scanner.Text()

			telemetry.ReportEvent(preFC.ctx, "vmm log",
				attribute.String("type", "stderr"),
				attribute.String("message", line),
			)
			fmt.Printf("[firecracker stderr]: %s — %v\n", preFC.ips.SlotIdx, line)
		}

		readerErr := scanner.Err()
		if readerErr != nil {
			errMsg := fmt.Errorf("error closing vmm stderr reader: %w", readerErr)
			telemetry.ReportError(preFC.ctx, errMsg)
			fmt.Printf("[firecracker stderr error]: %s — %v\n", preFC.ips.SlotIdx, errMsg)
		} else {
			telemetry.ReportEvent(preFC.ctx, "vmm stderr reader closed")
		}
	}()

	err = dns.Add(preFC.ips, config.SandboxID)
	if err != nil {
		errMsg := fmt.Errorf("failed to add DNS record: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	metadata := &MmdsMetadata{
		InstanceID: config.SandboxID,
		EnvID:      config.TemplateID,
		Address:    logsProxyAddress,
		TraceID:    traceID,
		TeamID:     config.TeamID,
	}

	err = preFC.loadSnapshot(childCtx, tracer, envFiles.EnvPath, metadata, preFC.sbxFiles.UFFDSocketPath)
	if err != nil {
		if uffd != nil {
			uffd.stop(childCtx, tracer)
		}

		errMsg := fmt.Errorf("failed to start FC: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "initialized FC")

	instance := &Sandbox{
		envFiles: envFiles,
		sbxFiles: preFC.sbxFiles,
		slot:     preFC.ips,
		fc:       preFC,
		uffd:     uffd,

		Sandbox: config,
	}

	telemetry.ReportEvent(childCtx, "ensuring clock sync")

	go func() {
		backgroundCtx := context.Background()

		clockErr := instance.EnsureClockSync(backgroundCtx)
		if clockErr != nil {
			telemetry.ReportError(backgroundCtx, fmt.Errorf("failed to sync clock: %w", clockErr))
		} else {
			telemetry.ReportEvent(backgroundCtx, "clock synced")
		}
	}()

	instance.StartedAt = time.Now()

	return instance, nil
}

func (s *Sandbox) syncClock(ctx context.Context) error {
	address := fmt.Sprintf("http://%s:%d/sync", s.slot.HostSnapshotIP(), consts.DefaultEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, "POST", address, nil)
	if err != nil {
		return err
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}

	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return err
	}

	defer response.Body.Close()

	return nil
}

func (s *Sandbox) EnsureClockSync(ctx context.Context) error {
syncLoop:
	for {
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		default:
			err := s.syncClock(ctx)
			if err != nil {
				telemetry.ReportError(ctx, fmt.Errorf("error syncing clock: %w", err))
				continue
			}
			break syncLoop
		}
	}

	return nil
}

func (s *Sandbox) CleanupAfterFCStop(
	ctx context.Context,
	tracer trace.Tracer,
	consul *consul.Client,
	dns *DNS,
	instanceID string,
) {
	childCtx, childSpan := tracer.Start(ctx, "delete-instance")
	defer childSpan.End()

	err := s.slot.RemoveNetwork(childCtx, tracer, dns, instanceID)
	if err != nil {
		errMsg := fmt.Errorf("cannot remove network when destroying task: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "removed network")
	}

	err = s.sbxFiles.Cleanup(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("failed to delete instance files: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "deleted instance files")
	}

	err = s.envFiles.Cleanup(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("failed to delete instance files: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "deleted instance files")
	}

	err = s.slot.Release(childCtx, tracer, consul)
	if err != nil {
		errMsg := fmt.Errorf("failed to release slot: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "released slot")
	}
}

func (s *Sandbox) Wait(ctx context.Context, tracer trace.Tracer) (err error) {
	defer s.Stop(ctx, tracer)

	if s.uffd != nil {
		go func() {
			err := s.uffd.wait()
			fmt.Printf("uffd wait error: %v", err)
		}()
	}

	return s.fc.cmd.Wait()
}

func (s *Sandbox) Stop(ctx context.Context, tracer trace.Tracer) {
	childCtx, childSpan := tracer.Start(ctx, "stop-sandbox", trace.WithAttributes())
	defer childSpan.End()

	s.fc.stop(childCtx, tracer)

	if s.uffd != nil {
		// Wait until we stop uffd if it exists
		time.Sleep(1 * time.Second)

		s.uffd.stop(childCtx, tracer)
	}
}

func (s *Sandbox) SlotIdx() int {
	return s.slot.SlotIdx
}

func (s *Sandbox) FcPid() int {
	return s.fc.cmd.Process.Pid
}

func (s *Sandbox) UffdPid() *int {
	if s.uffd == nil {
		return nil
	}

	return &s.uffd.pid
}
