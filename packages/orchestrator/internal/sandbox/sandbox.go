package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
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
	networkPool chan IPSlot,
	config *orchestrator.SandboxConfig,
	traceID string,
	startedAt time.Time,
	endAt time.Time,
	logger *logs.SandboxLogger,
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
			Address:    consts.LogsProxyAddress,
			TraceID:    traceID,
			TeamID:     config.TeamID,
		},
		pollReady,
	)

	err = fc.start(childCtx, tracer, logger)
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

	stats, err := NewSandboxStats(int32(fc.pid))
	if err != nil {
		return nil, fmt.Errorf("failed to create stats: %w", err)
	}

	stopped := make(chan struct{})

	instance := &Sandbox{
		files:     fsEnv,
		slot:      ips,
		fc:        fc,
		uffd:      fcUffd,
		Sandbox:   config,
		StartedAt: startedAt,
		EndAt:     endAt,
		Logger:    logger,
		stats:     stats,
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

			fcErr := fc.stop()

			if fcErr != nil || uffdErr != nil {
				return errors.Join(fcErr, uffdErr)
			}

			stopped <- struct{}{}
			return nil
		}),
	}

	telemetry.ReportEvent(childCtx, "ensuring clock sync")

	if semver.Compare(fmt.Sprintf("v%s", config.EnvdVersion), "v0.1.1") >= 0 {
		clockErr := instance.initRequest(ctx, consts.DefaultEnvdServerPort, config.EnvVars)
		if clockErr != nil {
			telemetry.ReportError(ctx, fmt.Errorf("failed to sync clock: %w", clockErr))
		} else {
			telemetry.ReportEvent(ctx, "clock synced")
		}
	} else {
		go func() {
			backgroundCtx := context.Background()

			clockErr := instance.EnsureClockSync(backgroundCtx, consts.OldEnvdServerPort)
			if clockErr != nil {
				telemetry.ReportError(backgroundCtx, fmt.Errorf("failed to sync clock (old envd): %w", clockErr))
			} else {
				telemetry.ReportEvent(backgroundCtx, "clock synced (old envd)")
			}
		}()
	}

	instance.StartedAt = time.Now()

	dns.Add(config.SandboxID, ips.HostIP())
	telemetry.ReportEvent(childCtx, "added DNS record", attribute.String("ip", ips.HostIP()), attribute.String("hostname", config.SandboxID))

	go func() {
		instance.logHeathAndUsage(stopped)
	}()

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

type PostInitJSONBody struct {
	EnvVars *map[string]string `json:"envVars"`
}

func (s *Sandbox) initRequest(ctx context.Context, port int64, envVars map[string]string) error {
	address := fmt.Sprintf("http://%s:%d/init", s.slot.HostSnapshotIP(), port)

	jsonBody := &PostInitJSONBody{
		EnvVars: &envVars,
	}
	envVarsJSON, err := json.Marshal(jsonBody)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, "POST", address, bytes.NewReader(envVarsJSON))
	if err != nil {
		return err
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}

	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", response.StatusCode)
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

func (s *Sandbox) logHeathAndUsage(exited chan struct{}) {
	ctx := context.Background()
	for {
		select {
		case <-time.After(10 * time.Second):
			childCtx, cancel := context.WithTimeout(ctx, time.Second)
			s.Healthcheck(childCtx, false)
			cancel()

			stats, err := s.stats.GetStats()
			if err != nil {
				s.Logger.Warnf("failed to get stats: %s", err)
			} else {
				s.Logger.CPUUsage(stats.CPUCount)
				s.Logger.MemoryUsage(stats.MemoryMB)
			}
		case <-exited:
			return
		}
	}
}

func (s *Sandbox) Healthcheck(ctx context.Context, alwaysReport bool) {
	var err error
	// Report healthcheck status
	defer s.Logger.Healthcheck(err == nil, alwaysReport)

	address := fmt.Sprintf("http://%s:%d/health", s.slot.HostSnapshotIP(), consts.DefaultEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		err = fmt.Errorf("unexpected status code: %d", response.StatusCode)
		return
	}

	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		return
	}
}
