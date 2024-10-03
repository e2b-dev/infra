package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"

	consul "github.com/hashicorp/consul/api"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	logsProxyAddress = os.Getenv("LOGS_PROXY_ADDRESS")

	httpClient = http.Client{}
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
	TraceID   string

	slot IPSlot
}

func NewSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	consul *consul.Client,
	dns *dns.DNS,
	networkPool chan IPSlot,
	templateCache *storage.TemplateDataCache,
	nbdPool *nbd.NbdDevicePool,
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

	// Get slot from Consul KV
	var ips IPSlot
	select {
	case ips = <-networkPool:
		telemetry.ReportEvent(childCtx, "reserved ip slot")
	case <-childCtx.Done():
		return nil, childCtx.Err()
	}

	networkSpan.End()

	defer func() {
		if err != nil {
			slotErr := ips.Release(childCtx, tracer, consul)
			if slotErr != nil {
				errMsg := fmt.Errorf("error removing network namespace after failed sandbox start: %w", slotErr)
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
				errMsg := fmt.Errorf("error removing network namespace after failed sandbox start: %w", ntErr)
				telemetry.ReportError(childCtx, errMsg)
			} else {
				telemetry.ReportEvent(childCtx, "removed network namespace")
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

	rootfs, err := storage.NewOverlayFile(childCtx, templateData.Rootfs, sandboxFiles.SandboxCacheRootfsPath(), nbdPool)
	if err != nil {
		return nil, fmt.Errorf("failed to create overlay file: %w", err)
	}

	telemetry.ReportEvent(childCtx, "created overlay file")

	defer func() {
		if err != nil {
			rootfsErr := rootfs.Close()
			if rootfsErr != nil {
				telemetry.ReportError(childCtx, fmt.Errorf("failed to close rootfs: %w", rootfsErr))
			}
		}
	}()

	go func() {
		runErr := rootfs.Run()
		if runErr != nil {
			errMsg := fmt.Errorf("failed to run rootfs: %w", err)
			fmt.Fprintf(os.Stderr, errMsg.Error())
		}
	}()

	telemetry.ReportEvent(childCtx, "started rootfs overlay for sandbox")

	fcUffd, err := uffd.New(templateData.Memfile, sandboxFiles.SandboxUffdSocketPath())
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

	fc := NewFC(
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
		fcUffd.PollReady,
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

	sbx := &Sandbox{
		files:     sandboxFiles,
		slot:      ips,
		fc:        fc,
		uffd:      fcUffd,
		Config:    config,
		StartedAt: startedAt,
		EndAt:     endAt,
		rootfs:    rootfs,
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

			return nil
		}),
	}

	telemetry.ReportEvent(childCtx, "ensuring clock sync")

	if semver.Compare(fmt.Sprintf("v%s", config.EnvdVersion), "v0.1.1") >= 0 {
		envdInitCtx, envdInitSpan := tracer.Start(childCtx, "envd-init")

		clockErr := sbx.initRequest(envdInitCtx, consts.DefaultEnvdServerPort, config.EnvVars)
		if clockErr != nil {
			telemetry.ReportCriticalError(envdInitCtx, fmt.Errorf("failed to sync clock: %w", clockErr))
		} else {
			telemetry.ReportEvent(envdInitCtx, "clock synced")
		}

		envdInitSpan.End()
	} else {
		go func() {
			ctx := context.Background()

			clockErr := sbx.EnsureClockSync(ctx, consts.OldEnvdServerPort)
			if clockErr != nil {
				telemetry.ReportError(ctx, fmt.Errorf("failed to sync clock (old envd): %w", clockErr))
			} else {
				telemetry.ReportEvent(ctx, "clock synced (old envd)")
			}
		}()
	}

	sbx.StartedAt = time.Now()

	dns.Add(config.SandboxId, ips.HostIP())
	telemetry.ReportEvent(childCtx, "added DNS record", attribute.String("ip", ips.HostIP()), attribute.String("hostname", config.SandboxId))

	return sbx, nil
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

	if _, copyErr := io.Copy(io.Discard, response.Body); copyErr != nil {
		return copyErr
	}

	defer response.Body.Close()

	return nil
}

func (s *Sandbox) EnsureClockSync(ctx context.Context, port int64) error {
syncLoop:
	for {
		select {
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
	childCtx, childSpan := tracer.Start(ctx, "delete-sandbox")
	defer childSpan.End()

	dns.Remove(sandboxID)
	telemetry.ReportEvent(childCtx, "removed sandbox from dns")

	err := s.slot.RemoveNetwork(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("cannot remove network when destroying task: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "removed network")
	}

	rootfsErr := s.rootfs.Close()
	if rootfsErr != nil {
		errMsg := fmt.Errorf("failed to close rootfs: %w", rootfsErr)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "closed rootfs overlay for sandbox")
	}

	for _, file := range []string{
		s.files.SandboxCacheDir(),
		s.files.SandboxFirecrackerSocketPath(),
		s.files.SandboxUffdSocketPath(),
	} {
		err = os.RemoveAll(file)
		if err != nil {
			errMsg := fmt.Errorf("failed to delete %s: %w", file, err)
			telemetry.ReportCriticalError(childCtx, errMsg)
		} else {
			telemetry.ReportEvent(childCtx, fmt.Sprintf("deleted %s", file))
		}
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
