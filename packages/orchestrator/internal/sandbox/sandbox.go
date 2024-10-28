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

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"

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

	process *fc.Process
	uffd    *uffd.Uffd
	rootfs  *storage.RootfsOverlay

	Config    *orchestrator.SandboxConfig
	StartedAt time.Time
	EndAt     time.Time

	TraceID string
	slot    network.IPSlot

	uffdExit chan error
}

// Run cleanup functions for the already initialized resources if there is any error or after you are done with the started sandbox.
func NewSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	dns *dns.DNS,
	networkPool *network.SlotPool,
	templateCache *storage.TemplateCache,
	config *orchestrator.SandboxConfig,
	traceID string,
	startedAt time.Time,
	endAt time.Time,
) (sbx *Sandbox, cleanup []func() error, err error) {
	childCtx, childSpan := tracer.Start(ctx, "new-sandbox")
	defer childSpan.End()

	template, err := templateCache.GetTemplate(
		config.TemplateId,
		config.BuildId,
		config.KernelVersion,
		config.FirecrackerVersion,
		config.HugePages,
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get template snapshot data: %w", err)
	}

	ips, poolErr := networkPool.Get(childCtx, tracer)
	if poolErr != nil {
		return nil, cleanup, fmt.Errorf("failed to get network slot: %w", poolErr)
	}

	cleanup = append(cleanup, func() error {
		releaseErr := networkPool.Release(ips)
		if releaseErr != nil {
			return fmt.Errorf("failed to release network slot: %w", releaseErr)
		}

		return nil
	})

	sandboxFiles := templateStorage.NewSandboxFiles(template.Files, config.SandboxId)

	cleanup = append(cleanup, func() error {
		filesErr := cleanupFiles(sandboxFiles)
		if filesErr != nil {
			return fmt.Errorf("failed to cleanup files: %w", filesErr)
		}

		return nil
	})

	err = os.MkdirAll(sandboxFiles.SandboxCacheDir(), 0o755)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to create sandbox cache dir: %w", err)
	}

	rootfs, err := template.NewRootfsOverlay(
		sandboxFiles.SandboxCacheRootfsPath(),
		sandboxFiles.SandboxNbdSocketPath(),
	)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to create overlay file: %w", err)
	}

	cleanup = append(cleanup, func() error {
		rootfsErr := rootfs.Close()
		if rootfsErr != nil {
			return fmt.Errorf("failed to close rootfs: %w", rootfsErr)
		}

		return nil
	})

	go func() {
		// TODO: Handle cleanup if failed.
		runErr := rootfs.Run(config.SandboxId)
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "[sandbox %s]: rootfs overlay error: %v\n", config.SandboxId, runErr)
		}
	}()

	memfile, err := template.Memfile()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get memfile: %w", err)
	}

	fcUffd, uffdErr := uffd.New(memfile, sandboxFiles.SandboxUffdSocketPath())
	if uffdErr != nil {
		return nil, cleanup, fmt.Errorf("failed to create uffd: %w", uffdErr)
	}

	uffdStartErr := fcUffd.Start(config.SandboxId)
	if uffdStartErr != nil {
		return nil, cleanup, fmt.Errorf("failed to start uffd: %w", uffdStartErr)
	}

	cleanup = append(cleanup, func() error {
		stopErr := fcUffd.Stop()
		if stopErr != nil {
			return fmt.Errorf("failed to stop uffd: %w", stopErr)
		}

		return nil
	})

	uffdExit := make(chan error, 1)
	childCtx, cancelFcUffd := context.WithCancelCause(childCtx)

	go func() {
		uffdWaitErr := fcUffd.Wait()
		uffdExit <- uffdWaitErr
		close(uffdExit)
		cancelFcUffd(fmt.Errorf("uffd process exited: %w", errors.Join(uffdWaitErr, context.Cause(childCtx))))
	}()

	snapfile, err := template.Snapfile()
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get snapfile: %w", err)
	}

	fcHandle, fcErr := fc.NewProcess(
		childCtx,
		tracer,
		ips,
		sandboxFiles,
		&fc.MmdsMetadata{
			SandboxId:            config.SandboxId,
			TemplateId:           config.TemplateId,
			LogsCollectorAddress: logsProxyAddress,
			TraceId:              traceID,
			TeamId:               config.TeamId,
		},
		snapfile,
		rootfs,
		fcUffd.Ready,
	)
	if fcErr != nil {
		return nil, cleanup, fmt.Errorf("failed to create FC: %w", fcErr)
	}

	fcStartErr := fcHandle.Start(childCtx, tracer)
	if fcStartErr != nil {
		return nil, cleanup, fmt.Errorf("failed to start FC: %w", fcStartErr)
	}

	cleanup = append(cleanup, func() error {
		stopErr := fcHandle.Stop()
		if stopErr != nil {
			return fmt.Errorf("failed to stop FC: %w", stopErr)
		}

		return nil
	})

	sbx = &Sandbox{
		uffdExit:  uffdExit,
		files:     sandboxFiles,
		slot:      ips,
		process:   fcHandle,
		uffd:      fcUffd,
		Config:    config,
		StartedAt: startedAt,
		EndAt:     endAt,
		rootfs:    rootfs,
		stopOnce: sync.OnceValue(func() error {
			var errs []error

			fcStopErr := fcHandle.Stop()
			if fcStopErr != nil {
				errs = append(errs, fmt.Errorf("failed to stop FC: %w", fcStopErr))
			}

			uffdStopErr := fcUffd.Stop()
			if uffdStopErr != nil {
				errs = append(errs, fmt.Errorf("failed to stop uffd: %w", uffdStopErr))
			}

			return errors.Join(errs...)
		}),
	}

	if semver.Compare(fmt.Sprintf("v%s", config.EnvdVersion), "v0.1.1") >= 0 {
		initErr := sbx.initEnvd(childCtx, tracer, config.EnvVars)
		if initErr != nil {
			return nil, cleanup, fmt.Errorf("failed to init new envd: %w", initErr)
		} else {
			telemetry.ReportEvent(childCtx, fmt.Sprintf("[sandbox %s]: initialized new envd", config.SandboxId))
		}
	} else {
		syncErr := sbx.syncOldEnvd(childCtx)
		if syncErr != nil {
			telemetry.ReportError(childCtx, fmt.Errorf("failed to sync old envd: %w", syncErr))
		} else {
			telemetry.ReportEvent(childCtx, fmt.Sprintf("[sandbox %s]: synced old envd", config.SandboxId))
		}
	}

	sbx.StartedAt = time.Now()

	dns.Add(config.SandboxId, ips.HostIP())

	cleanup = append(cleanup, func() error {
		dns.Remove(config.SandboxId)

		return nil
	})

	return sbx, cleanup, nil
}

func (s *Sandbox) Wait() error {
	select {
	case fcErr := <-s.process.Exit:
		stopErr := s.Stop()
		uffdErr := <-s.uffdExit

		return errors.Join(fcErr, stopErr, uffdErr)
	case uffdErr := <-s.uffdExit:
		stopErr := s.Stop()
		fcErr := <-s.process.Exit

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
