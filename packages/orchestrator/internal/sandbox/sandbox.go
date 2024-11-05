package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	consul "github.com/hashicorp/consul/api"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/mod/semver"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	fcVersionsDir  = "/fc-versions"
	kernelsDir     = "/fc-kernels"
	kernelMountDir = "/fc-vm"
	kernelName     = "vmlinux.bin"
	fcBinaryName   = "firecracker"
)

var httpClient = http.Client{
	Timeout: 5 * time.Second,
}

type Sandbox struct {
	files    *SandboxFiles
	stopOnce func() error

	fc   *fc
	uffd *uffd.Uffd

	Sandbox   *orchestrator.SandboxConfig
	StartedAt time.Time
	EndAt     time.Time
	TraceID   string

	networkPool *NetworkSlotPool

	slot   IPSlot
	Logger *logs.SandboxLogger
	stats  *SandboxStats
}

func fcBinaryPath(fcVersion string) string {
	return filepath.Join(fcVersionsDir, fcVersion, fcBinaryName)
}

func NewSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	consul *consul.Client,
	dns *dns.DNS,
	networkPool *NetworkSlotPool,
	config *orchestrator.SandboxConfig,
	traceID string,
	startedAt time.Time,
	endAt time.Time,
	logger *logs.SandboxLogger,
) (*Sandbox, error) {
	childCtx, childSpan := tracer.Start(ctx, "new-sandbox")
	defer childSpan.End()

	networkCtx, networkSpan := tracer.Start(childCtx, "get-network-slot")
	// Get slot from Consul KV

	ips, err := networkPool.Get(networkCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get network slot: %w", err)
	}

	networkSpan.End()

	internalLogger := logs.NewSandboxLogger(config.SandboxID, config.TemplateID, config.TeamID, config.VCpuCount, config.MemoryMB, true)

	defer func() {
		if err != nil {
			networkPool.Return(consul, ips)
			internalLogger.Debugf("returned ip slot")
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
				internalLogger.Errorf("error deleting env after failed fc start: %s", err)
			} else {
				telemetry.ReportEvent(childCtx, "deleted env")
				internalLogger.Debugf("deleted env")
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

		uffdErr := fcUffd.Start(childCtx, tracer, logger)
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
			Address:    consts.LogsProxyAddress,
			TraceID:    traceID,
			TeamID:     config.TeamID,
		},
		pollReady,
	)

	err = fc.start(childCtx, tracer, internalLogger)
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

	stats := newSandboxStats(int32(fc.pid))
	if err != nil {
		return nil, fmt.Errorf("failed to create stats: %w", err)
	}

	healthcheckCtx := utils.NewLockableCancelableContext(context.Background())

	instance := &Sandbox{
		files:       fsEnv,
		slot:        ips,
		fc:          fc,
		uffd:        fcUffd,
		Sandbox:     config,
		StartedAt:   startedAt,
		networkPool: networkPool,
		EndAt:       endAt,
		Logger:      logger,
		stats:       stats,
		stopOnce: sync.OnceValue(func() error {
			var uffdErr error
			if fcUffd != nil {
				// Wait until we stop uffd if it exists
				time.Sleep(1 * time.Second)

				uffdErr = fcUffd.Stop()
				if uffdErr != nil {
					uffdErr = fmt.Errorf("failed to stop uffd: %w", err)
				}
			}

			healthcheckCtx.Lock()
			healthcheckCtx.Cancel()
			healthcheckCtx.Unlock()

			fcErr := fc.stop()

			if fcErr != nil || uffdErr != nil {
				return errors.Join(fcErr, uffdErr)
			}

			return nil
		}),
	}

	telemetry.ReportEvent(childCtx, "ensuring clock sync")

	uffdStartCtx, initSpan := tracer.Start(childCtx, "ensure-clock-sync")

	// Sync envds.
	if semver.Compare(fmt.Sprintf("v%s", config.EnvdVersion), "v0.1.1") >= 0 {
		initErr := instance.initEnvd(uffdStartCtx, tracer, config.EnvVars)
		if initErr != nil {
			return nil, fmt.Errorf("failed to init new envd: %w", initErr)
		} else {
			telemetry.ReportEvent(uffdStartCtx, fmt.Sprintf("[sandbox %s]: initialized new envd", config.SandboxID))
		}
	} else {
		syncErr := instance.syncOldEnvd(uffdStartCtx)
		if syncErr != nil {
			telemetry.ReportError(uffdStartCtx, fmt.Errorf("failed to sync old envd: %w", syncErr))
		} else {
			telemetry.ReportEvent(uffdStartCtx, fmt.Sprintf("[sandbox %s]: synced old envd", config.SandboxID))
		}
	}
	initSpan.End()

	instance.StartedAt = time.Now()

	dns.Add(config.SandboxID, ips.HostIP())
	telemetry.ReportEvent(childCtx, "added DNS record", attribute.String("ip", ips.HostIP()), attribute.String("hostname", config.SandboxID))

	go func() {
		instance.logHeathAndUsage(healthcheckCtx)
	}()

	return instance, nil
}

func (s *Sandbox) syncClock(ctx context.Context, port int64) error {
	address := fmt.Sprintf("http://%s:%d/sync", s.slot.HostIP(), port)

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

	s.networkPool.Return(consul, s.slot)
	err := s.files.Cleanup(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("failed to delete instance files: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "deleted instance files")
	}
}

func (s *Sandbox) Wait(ctx context.Context, tracer trace.Tracer) error {
	uffdExit := make(chan error)
	fcExit := make(chan error)

	go func() {
		fcExit <- s.fc.wait()
		close(fcExit)
	}()

	if s.uffd != nil {
		go func() {
			uffdExit <- s.uffd.Wait()
			close(uffdExit)
		}()
	}

	select {
	case fcErr := <-fcExit:
		stopErr := s.Stop(ctx, tracer)
		uffdErr := <-uffdExit

		return errors.Join(fcErr, stopErr, uffdErr)
	case uffdErr := <-uffdExit:
		stopErr := s.Stop(ctx, tracer)
		fcErr := <-fcExit

		return errors.Join(uffdErr, stopErr, fcErr)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Sandbox) Stop(ctx context.Context, tracer trace.Tracer) error {
	childCtx, childSpan := tracer.Start(ctx, "stop-sandbox", trace.WithAttributes())
	defer childSpan.End()

	err := s.stopOnce()
	if err != nil {
		return fmt.Errorf("failed to stop sandbox: %w", err)
	}

	telemetry.ReportEvent(childCtx, "stopped sandbox")

	return nil
}

func (s *Sandbox) SlotIdx() int {
	return s.slot.SlotIdx
}

func (s *Sandbox) FcPid() int {
	return s.fc.pid
}
