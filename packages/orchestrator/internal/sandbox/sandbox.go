package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/mod/semver"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	localStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/local_storage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
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

	networkPool *network.SlotPool

	slot   network.IPSlot
	Logger *logs.SandboxLogger
	stats  *SandboxStats
}

func fcBinaryPath(fcVersion string) string {
	return filepath.Join(fcVersionsDir, fcVersion, fcBinaryName)
}

func NewSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	dns *dns.DNS,
	networkPool *network.SlotPool,
	templateCache *localStorage.TemplateCache,
	config *orchestrator.SandboxConfig,
	traceID string,
	startedAt time.Time,
	endAt time.Time,
	logger *logs.SandboxLogger,
) (sbx *Sandbox, cleanup []func() error, err error) {
	childCtx, childSpan := tracer.Start(ctx, "new-sandbox")
	defer childSpan.End()

	tmpl, err := templateCache.GetTemplate(
		config.TemplateId,
		config.BuildId,
		config.KernelVersion,
		config.FirecrackerVersion,
		config.HugePages,
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get template snapshot data: %w", err)
	}

	networkCtx, networkSpan := tracer.Start(childCtx, "get-network-slot")
	// Get slot from Consul KV

	ips, err := networkPool.Get(networkCtx)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get network slot: %w", err)
	}

	cleanup = append(cleanup, func() error {
		networkPool.Return(ips)

		return nil
	})

	networkSpan.End()

	internalLogger := logs.NewSandboxLogger(config.SandboxId, config.TemplateId, config.TeamId, config.Vcpu, config.RamMb, true)

	fsEnv, err := newSandboxFiles(
		childCtx,
		tracer,
		config.SandboxId,
		config.TemplateId,
		config.BuildId,
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

		return nil, cleanup, errMsg
	}

	cleanup = append(cleanup, func() error {
		err = fsEnv.Cleanup(childCtx)
		if err != nil {
			errMsg := fmt.Errorf("failed to delete instance files: %w", err)
			telemetry.ReportCriticalError(childCtx, errMsg)
		} else {
			telemetry.ReportEvent(childCtx, "deleted instance files")
		}

		return nil
	})

	telemetry.ReportEvent(childCtx, "assembled env files info")

	err = fsEnv.Ensure(childCtx)
	if err != nil {
		errMsg := fmt.Errorf("failed to create env for FC: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, cleanup, errMsg
	}

	telemetry.ReportEvent(childCtx, "created env for FC")

	memfile, err := tmpl.Memfile()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get memfile: %w", err)
	}

	var fcUffd *uffd.Uffd
	if fsEnv.UFFDSocketPath != nil {
		fcUffd, err = uffd.New(memfile, *fsEnv.UFFDSocketPath, config.TemplateId, config.BuildId)
		if err != nil {
			return nil, cleanup, fmt.Errorf("failed to create uffd: %w", err)
		}

		telemetry.ReportEvent(childCtx, "created uffd")

		uffdErr := fcUffd.Start(childCtx, tracer, logger)
		if err != nil {
			errMsg := fmt.Errorf("failed to start uffd: %w", uffdErr)
			telemetry.ReportCriticalError(childCtx, errMsg)

			return nil, cleanup, errMsg
		}

		telemetry.ReportEvent(childCtx, "started uffd")
	}

	var pollReady chan struct{}
	if fcUffd != nil {
		pollReady = fcUffd.PollReady
	}

	cleanup = append(cleanup, func() error {
		stopErr := fcUffd.Stop()
		if stopErr != nil {
			return fmt.Errorf("failed to stop uffd: %w", stopErr)
		}

		return nil
	})

	overlayCtx, overlaySpan := tracer.Start(childCtx, "create-rootfs-overlay")
	fsOverlay, err := tmpl.NewRootfsOverlay(filepath.Join(os.TempDir(), fmt.Sprintf("rootfs-%s-overlay.img", config.SandboxId)))
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to create rootfs overlay: %w", err)
	}

	cleanup = append(cleanup, func() error {
		fsOverlay.Close()

		return nil
	})

	go func() {
		overlayErr := fsOverlay.Run()
		if overlayErr != nil {
			fmt.Fprintf(os.Stderr, "failed to run overlay: %v\n", overlayErr)
		}
	}()

	overlayPath, err := fsOverlay.Path(overlayCtx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting overlay path: %v\n", err)

		return nil, cleanup, err
	}
	overlaySpan.End()

	fc := newFC(
		childCtx,
		tracer,
		ips,
		fsEnv,
		&MmdsMetadata{
			InstanceID: config.SandboxId,
			EnvID:      config.TemplateId,
			Address:    logs.LogsCollectorAddress,
			TraceID:    traceID,
			TeamID:     config.TeamId,
		},
		pollReady,
		overlayPath,
	)

	snapfile, err := tmpl.Snapfile()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get snapfile: %w", err)
	}

	err = fc.start(childCtx, tracer, internalLogger, snapfile)
	if err != nil {
		errMsg := fmt.Errorf("failed to start FC: %w", err)

		return nil, cleanup, errMsg
	}

	telemetry.ReportEvent(childCtx, "initialized FC")

	stats := newSandboxStats(int32(fc.pid))
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to create stats: %w", err)
	}

	healthcheckCtx := utils.NewLockableCancelableContext(context.Background())

	sbx = &Sandbox{
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

	// Ensure the syncing takes at most 10 seconds.
	syncCtx, syncCancel := context.WithTimeout(childCtx, 10*time.Second)
	defer syncCancel()

	// Sync envds.
	if semver.Compare(fmt.Sprintf("v%s", config.EnvdVersion), "v0.1.1") >= 0 {
		initErr := sbx.initEnvd(syncCtx, tracer, config.EnvVars, overlayPath)
		if initErr != nil {
			return nil, cleanup, fmt.Errorf("failed to init new envd: %w", initErr)
		} else {
			telemetry.ReportEvent(childCtx, fmt.Sprintf("[sandbox %s]: initialized new envd", config.SandboxId))
		}
	} else {
		syncErr := sbx.syncOldEnvd(syncCtx)
		if syncErr != nil {
			telemetry.ReportError(childCtx, fmt.Errorf("failed to sync old envd: %w", syncErr))
		} else {
			telemetry.ReportEvent(childCtx, fmt.Sprintf("[sandbox %s]: synced old envd", config.SandboxId))
		}
	}

	sbx.StartedAt = time.Now()

	dns.Add(config.SandboxId, ips.HostIP())
	telemetry.ReportEvent(childCtx, "added DNS record", attribute.String("ip", ips.HostIP()), attribute.String("hostname", config.SandboxId))
	cleanup = append(cleanup, func() error {
		dns.Remove(config.SandboxId)

		return nil
	})

	go func() {
		sbx.logHeathAndUsage(healthcheckCtx)
	}()

	return sbx, cleanup, nil
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
