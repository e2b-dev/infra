package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"

	consul "github.com/hashicorp/consul/api"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	fcVersionsDir  = "/fc-versions"
	kernelsDir     = "/fc-kernels"
	kernelMountDir = "/fc-vm"
	kernelName     = "vmlinux.bin"
	fcBinaryName   = "firecracker"
)

var logsProxyAddress = os.Getenv("LOGS_PROXY_ADDRESS")

var httpClient = http.Client{
	Timeout: 5 * time.Second,
}

type Sandbox struct {
	files *SandboxFiles

	fc   *fc
	uffd *uffd.Uffd

	Sandbox   *orchestrator.SandboxConfig
	StartedAt time.Time
	TraceID   string

	slot IPSlot
}

func fcBinaryPath(fcVersion string) string {
	return filepath.Join(fcVersionsDir, fcVersion, fcBinaryName)
}

func NewSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	consul *consul.Client,
	dns *dns.DNS,
	networkPool chan IPSlot,
	config *orchestrator.SandboxConfig,
	traceID string,
) (*Sandbox, error) {
	childCtx, childSpan := tracer.Start(ctx, "new-sandbox")
	defer childSpan.End()

	_, networkSpan := tracer.Start(childCtx, "get-network-slot")
	// Get slot from Consul KV

	var ips IPSlot
	select {
	case ips = <-networkPool:
		telemetry.ReportEvent(childCtx, "reserved ip slot")
	case <-childCtx.Done():
		return nil, childCtx.Err()
	}

	networkSpan.End()

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

	defer func() {
		if err != nil {
			ntErr := ips.RemoveNetwork(childCtx, tracer)
			if ntErr != nil {
				errMsg := fmt.Errorf("error removing network namespace after failed instance start: %w", ntErr)
				telemetry.ReportError(childCtx, errMsg)
			} else {
				telemetry.ReportEvent(childCtx, "removed network namespace")
			}
		}
	}()

	fsEnv, err := newSandboxFiles(
		childCtx,
		tracer,
		config.SandboxID,
		config.TemplateID,
		config.KernelVersion,
		kernelsDir,
		kernelMountDir,
		kernelName,
		fcBinaryPath(config.FirecrackerVersion),
		config.HugePages,
	)
	if err != nil {
		errMsg := fmt.Errorf("failed to assemble env files info for FC: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "assembled env files info")

	err = fsEnv.Ensure(childCtx)
	if err != nil {
		errMsg := fmt.Errorf("failed to create env for FC: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "created env for FC")

	defer func() {
		if err != nil {
			envErr := fsEnv.Cleanup(childCtx, tracer)
			if envErr != nil {
				errMsg := fmt.Errorf("error deleting env after failed fc start: %w", err)
				telemetry.ReportCriticalError(childCtx, errMsg)
			} else {
				telemetry.ReportEvent(childCtx, "deleted env")
			}
		}
	}()

	var fcUffd *uffd.Uffd
	if fsEnv.UFFDSocketPath != nil {
		fcUffd, err = uffd.New(fsEnv.MemfilePath(), *fsEnv.UFFDSocketPath, config.TemplateID, config.BuildID)
		if err != nil {
			return nil, fmt.Errorf("failed to create uffd: %w", err)
		}

		telemetry.ReportEvent(childCtx, "created uffd")

		uffdErr := fcUffd.Start(childCtx, tracer)
		if err != nil {
			errMsg := fmt.Errorf("failed to start uffd: %w", uffdErr)
			telemetry.ReportCriticalError(childCtx, errMsg)

			return nil, errMsg
		}

		telemetry.ReportEvent(childCtx, "started uffd")
	}

	var pollReady chan struct{}
	if fcUffd != nil {
		pollReady = fcUffd.PollReady
	}

	fc := newFC(
		childCtx,
		tracer,
		ips,
		fsEnv,
		&MmdsMetadata{
			InstanceID: config.SandboxID,
			EnvID:      config.TemplateID,
			Address:    logsProxyAddress,
			TraceID:    traceID,
			TeamID:     config.TeamID,
		},
		pollReady,
	)

	err = fc.start(childCtx, tracer)
	if err != nil {
		var fcUffdErr error
		if fcUffd != nil {
			fcUffdErr = fcUffd.Stop()
		}

		errMsg := fmt.Errorf("failed to start FC: %w", err)
		telemetry.ReportCriticalError(childCtx, errors.Join(errMsg, fcUffdErr))

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "initialized FC")

	instance := &Sandbox{
		files: fsEnv,
		slot:  ips,
		fc:    fc,
		uffd:  fcUffd,

		Sandbox: config,
	}

	telemetry.ReportEvent(childCtx, "ensuring clock sync")

	// TODO: Switch to using the sync in the new envd
	go func() {
		backgroundCtx := context.Background()

		clockErr := instance.EnsureClockSync(backgroundCtx, consts.OldEnvdServerPort)
		if clockErr != nil {
			telemetry.ReportError(backgroundCtx, fmt.Errorf("failed to sync clock (old envd): %w", clockErr))
		} else {
			telemetry.ReportEvent(backgroundCtx, "clock synced (old envd)")
		}
	}()

	instance.StartedAt = time.Now()

	dns.Add(config.SandboxID, ips.HostIP())
	telemetry.ReportEvent(childCtx, "added DNS record", attribute.String("ip", ips.HostIP()), attribute.String("hostname", config.SandboxID))

	return instance, nil
}

func (s *Sandbox) syncClock(ctx context.Context, port int64) error {
	address := fmt.Sprintf("http://%s:%d/sync", s.slot.HostSnapshotIP(), port)

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

func (s *Sandbox) EnsureClockSync(ctx context.Context, port int64) error {
syncLoop:
	for {
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		default:
			err := s.syncClock(ctx, port)
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
	dns *dns.DNS,
	sandboxID string,
) {
	childCtx, childSpan := tracer.Start(ctx, "delete-instance")
	defer childSpan.End()

	dns.Remove(sandboxID)
	telemetry.ReportEvent(childCtx, "removed env instance to etc hosts")

	err := s.slot.RemoveNetwork(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("cannot remove network when destroying task: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "removed network")
	}

	err = s.files.Cleanup(childCtx, tracer)
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

func (s *Sandbox) Wait(ctx context.Context, tracer trace.Tracer) error {
	defer func() {
		err := s.Stop(ctx, tracer)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error when stopping sandbox: %v\n", err)
		}
	}()

	uffdExit := make(chan error)

	if s.uffd != nil {
		go func() {
			uffdExit <- s.uffd.Wait()
			close(uffdExit)
		}()
	}

	// TODO: Exit fc on uffd exit and exit uffd on fc exit?

	return s.fc.wait()
}

func (s *Sandbox) Stop(ctx context.Context, tracer trace.Tracer) error {
	childCtx, childSpan := tracer.Start(ctx, "stop-sandbox", trace.WithAttributes())
	defer childSpan.End()

	s.fc.stop(childCtx, tracer)

	telemetry.ReportEvent(childCtx, "stopped fc process")

	if s.uffd != nil {
		// Wait until we stop uffd if it exists
		time.Sleep(1 * time.Second)

		err := s.uffd.Stop()
		if err != nil {
			return fmt.Errorf("failed to stop uffd: %w", err)
		}

		telemetry.ReportEvent(childCtx, "stopped uffd")
	}

	return nil
}

func (s *Sandbox) SlotIdx() int {
	return s.slot.SlotIdx
}

func (s *Sandbox) FcPid() int {
	return s.fc.pid
}
