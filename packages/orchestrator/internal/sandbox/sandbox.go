package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"

	consul "github.com/hashicorp/consul/api"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	logsProxyAddress = os.Getenv("LOGS_PROXY_ADDRESS")

	httpClient = http.Client{
		Timeout: 10 * time.Second,
	}
)

type Sandbox struct {
	files    *templateStorage.SandboxFiles
	stopOnce func() error

	fc     *fc
	uffd   *uffd.Uffd
	rootfs *storage.OverlayFile

	Config    *orchestrator.SandboxConfig
	StartedAt time.Time
	EndAt     time.Time

	networkPool *NetworkSlotPool
	TraceID     string
	slot        IPSlot
}

func NewSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	consul *consul.Client,
	dns *dns.DNS,
	networkPool *NetworkSlotPool,
	templateCache *storage.TemplateDataCache,
	nbdPool *nbd.DevicePool,
	config *orchestrator.SandboxConfig,
	traceID string,
	startedAt time.Time,
	endAt time.Time,
) (*Sandbox, error) {
	childCtx, childSpan := tracer.Start(ctx, "new-sandbox")
	defer childSpan.End()

	templateData, err := templateCache.GetTemplateData(
		config.TemplateId,
		config.BuildId,
		config.KernelVersion,
		config.FirecrackerVersion,
		config.HugePages,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get template snapshot data: %w", err)
	}

	telemetry.ReportEvent(childCtx, "got template snapshot data")

	_, networkSpan := tracer.Start(childCtx, "get-network-slot")

	ips, err := networkPool.Get(childCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get network slot: %w", err)
	}

	networkSpan.End()

	defer func() {
		if err != nil {
			errMsg := networkPool.Release(consul, ips)
			if errMsg != nil {
				telemetry.ReportError(childCtx, errMsg)
			}
		}
	}()

	sandboxFiles := templateStorage.NewSandboxFiles(templateData.Files, config.SandboxId)

	err = os.MkdirAll(sandboxFiles.SandboxCacheDir(), 0o755)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox cache dir: %w", err)
	}

	telemetry.ReportEvent(childCtx, "created sandbox cache dir")

	defer func() {
		if err != nil {
			cacheRmErr := os.RemoveAll(sandboxFiles.SandboxCacheDir())
			firecrackerSocketCacheRmErr := os.RemoveAll(sandboxFiles.SandboxFirecrackerSocketPath())
			uffdSocketCacheRmErr := os.RemoveAll(sandboxFiles.SandboxUffdSocketPath())

			err = errors.Join(cacheRmErr, firecrackerSocketCacheRmErr, uffdSocketCacheRmErr, err)

			telemetry.ReportError(childCtx, fmt.Errorf("removing sandbox cache dir: %w", err))
		}
	}()

	rootfs, err := storage.NewOverlayFile(
		templateData.Rootfs,
		sandboxFiles.SandboxCacheRootfsPath(),
		nbdPool,
		sandboxFiles.SandboxNbdSocketPath(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create overlay file: %w", err)
	}

	telemetry.ReportEvent(childCtx, "created overlay file")

	go func() {
		// TODO: Handle cleanup if failed.
		runErr := rootfs.Run()
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "[sandbox %s]: rootfs overlay error: %v\n", config.SandboxId, runErr)
		}
	}()

	defer func() {
		if err != nil {
			rootfsErr := rootfs.Close()
			if rootfsErr != nil {
				telemetry.ReportError(childCtx, fmt.Errorf("failed to close rootfs: %w", rootfsErr))
			}
		}
	}()

	telemetry.ReportEvent(childCtx, "started rootfs overlay for sandbox")

	fcUffd, err := uffd.New(templateData.Memfile, sandboxFiles.SandboxUffdSocketPath())
	if err != nil {
		return nil, fmt.Errorf("failed to create uffd: %w", err)
	}

	telemetry.ReportEvent(childCtx, "created uffd")

	uffdErr := fcUffd.Start(childCtx, tracer, config.SandboxId)
	if uffdErr != nil {
		errMsg := fmt.Errorf("failed to start uffd: %w", uffdErr)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "started uffd")

	fc, err := NewFC(
		childCtx,
		tracer,
		ips,
		sandboxFiles,
		&MmdsMetadata{
			SandboxId:            config.SandboxId,
			TemplateId:           config.TemplateId,
			LogsCollectorAddress: logsProxyAddress,
			TraceId:              traceID,
			TeamId:               config.TeamId,
		},
		templateData.Snapfile,
		rootfs,
		fcUffd.Ready,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create FC: %w", err)
	}

	err = fc.start(childCtx, tracer)
	if err != nil {
		fcUffdErr := fcUffd.Stop()

		errMsg := fmt.Errorf("failed to start FC: %w", err)
		telemetry.ReportCriticalError(childCtx, errors.Join(errMsg, fcUffdErr))

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "initialized FC")

	sbx := &Sandbox{
		files:       sandboxFiles,
		slot:        ips,
		fc:          fc,
		uffd:        fcUffd,
		Config:      config,
		StartedAt:   startedAt,
		EndAt:       endAt,
		rootfs:      rootfs,
		networkPool: networkPool,
		stopOnce: sync.OnceValue(func() error {
			var errs []error

			uffdErr := fcUffd.Stop()
			if uffdErr != nil {
				errs = append(errs, fmt.Errorf("failed to stop uffd: %w", uffdErr))
			}

			fcErr := fc.stop()
			if fcErr != nil {
				errs = append(errs, fmt.Errorf("failed to stop FC: %w", fcErr))
			}

			return errors.Join(errs...)
		}),
	}

	telemetry.ReportEvent(childCtx, "ensuring clock sync")

	if semver.Compare(fmt.Sprintf("v%s", config.EnvdVersion), "v0.1.1") >= 0 {
		envdInitCtx, envdInitSpan := tracer.Start(childCtx, "envd-init")

		initErr := sbx.initEnvd(envdInitCtx, config.EnvVars)
		if initErr != nil {
			telemetry.ReportCriticalError(envdInitCtx, fmt.Errorf("failed to init new envd: %w", initErr))

			return nil, fmt.Errorf("failed to init new envd: %w", initErr)
		} else {
			telemetry.ReportEvent(envdInitCtx, "new envd initialized")
		}

		envdInitSpan.End()
	} else {
		go func() {
			syncErr := sbx.syncOldEnvd(ctx)
			if syncErr != nil {
				telemetry.ReportError(ctx, fmt.Errorf("failed to sync old envd: %w", syncErr))
			} else {
				telemetry.ReportEvent(ctx, "old envd synced")
			}
		}()
	}

	sbx.StartedAt = time.Now()

	dns.Add(config.SandboxId, ips.HostIP())
	telemetry.ReportEvent(childCtx, "added DNS record", attribute.String("ip", ips.HostIP()), attribute.String("hostname", config.SandboxId))

	return sbx, nil
}

func (s *Sandbox) CleanupAfterFCStop(
	consul *consul.Client,
	dns *dns.DNS,
	sandboxID string,
) error {
	var errs []error

	dns.Remove(sandboxID)

	rootfsErr := s.rootfs.Close()
	if rootfsErr != nil {
		errs = append(errs, fmt.Errorf("failed to close rootfs: %w", rootfsErr))
	}

	for _, file := range []string{
		s.files.SandboxCacheDir(),
		s.files.SandboxFirecrackerSocketPath(),
		s.files.SandboxUffdSocketPath(),
	} {
		err := os.RemoveAll(file)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to delete %s: %w", file, err))
		}
	}

	err := s.networkPool.Release(consul, s.slot)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot: %w", err))
	}

	return errors.Join(errs...)
}

func (s *Sandbox) Wait() error {
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
		stopErr := s.Stop()
		uffdErr := <-uffdExit

		return errors.Join(fcErr, stopErr, uffdErr)
	case uffdErr := <-uffdExit:
		stopErr := s.Stop()
		fcErr := <-fcExit

		return errors.Join(uffdErr, stopErr, fcErr)
	}
}

func (s *Sandbox) Stop() error {
	err := s.stopOnce()
	if err != nil {
		return fmt.Errorf("failed to stop sandbox: %w", err)
	}

	return nil
}

func (s *Sandbox) SlotIdx() int {
	return s.slot.SlotIdx
}
