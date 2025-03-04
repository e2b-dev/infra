package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	requestTimeout = 60 * time.Second

	maxParalellSnapshotting = 8
)

func (s *server) Create(ctxConn context.Context, req *orchestrator.SandboxCreateRequest) (*orchestrator.SandboxCreateResponse, error) {
	ctx, cancel := context.WithTimeoutCause(ctxConn, requestTimeout, fmt.Errorf("request timed out"))
	defer cancel()

	childCtx, childSpan := s.tracer.Start(ctx, "sandbox-create")
	defer childSpan.End()

	childSpan.SetAttributes(
		attribute.String("template.id", req.Sandbox.TemplateId),
		attribute.String("kernel.version", req.Sandbox.KernelVersion),
		attribute.String("sandbox.id", req.Sandbox.SandboxId),
		attribute.String("client.id", consul.ClientID),
		attribute.String("envd.version", req.Sandbox.EnvdVersion),
	)

	logger := logs.NewSandboxLogger(
		req.Sandbox.SandboxId,
		req.Sandbox.TemplateId,
		req.Sandbox.TeamId,
		req.Sandbox.Vcpu,
		req.Sandbox.RamMb,
		false,
	)

	sbx, cleanup, err := sandbox.NewSandbox(
		childCtx,
		s.tracer,
		s.dns,
		s.networkPool,
		s.templateCache,
		req.Sandbox,
		childSpan.SpanContext().TraceID().String(),
		req.StartTime.AsTime(),
		req.EndTime.AsTime(),
		logger,
		req.Sandbox.Snapshot,
		req.Sandbox.BaseTemplateId,
	)
	if err != nil {
		log.Printf("failed to create sandbox -> clean up: %v", err)
		cleanupErr := cleanup.Run()

		errMsg := fmt.Errorf("failed to create sandbox: %w", errors.Join(err, context.Cause(ctx), cleanupErr))
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, status.New(codes.Internal, errMsg.Error()).Err()
	}

	s.sandboxes.Insert(req.Sandbox.SandboxId, sbx)

	go func() {
		waitErr := sbx.Wait()
		if waitErr != nil {
			fmt.Fprintf(os.Stderr, "failed to wait for Sandbox: %v\n", waitErr)
		}

		cleanupErr := cleanup.Run()
		if cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "failed to cleanup Sandbox: %v\n", cleanupErr)
		}

		s.sandboxes.Remove(req.Sandbox.SandboxId)

		logger.Infof("Sandbox killed")
	}()

	return &orchestrator.SandboxCreateResponse{
		ClientId: consul.ClientID,
	}, nil
}

func (s *server) Update(ctx context.Context, req *orchestrator.SandboxUpdateRequest) (*emptypb.Empty, error) {
	ctx, childSpan := s.tracer.Start(ctx, "sandbox-update")
	defer childSpan.End()

	childSpan.SetAttributes(
		attribute.String("sandbox.id", req.SandboxId),
		attribute.String("client.id", consul.ClientID),
	)

	item, ok := s.sandboxes.Get(req.SandboxId)
	if !ok {
		errMsg := fmt.Errorf("sandbox not found")
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, status.New(codes.NotFound, errMsg.Error()).Err()
	}

	item.EndAt = req.EndTime.AsTime()

	return &emptypb.Empty{}, nil
}

func (s *server) List(ctx context.Context, _ *emptypb.Empty) (*orchestrator.SandboxListResponse, error) {
	_, childSpan := s.tracer.Start(ctx, "sandbox-list")
	defer childSpan.End()

	items := s.sandboxes.Items()

	sandboxes := make([]*orchestrator.RunningSandbox, 0, len(items))

	for _, sbx := range items {
		if sbx == nil {
			continue
		}

		if sbx.Config == nil {
			continue
		}

		sandboxes = append(sandboxes, &orchestrator.RunningSandbox{
			Config:    sbx.Config,
			ClientId:  consul.ClientID,
			StartTime: timestamppb.New(sbx.StartedAt),
			EndTime:   timestamppb.New(sbx.EndAt),
		})
	}

	return &orchestrator.SandboxListResponse{
		Sandboxes: sandboxes,
	}, nil
}

func (s *server) Delete(ctxConn context.Context, in *orchestrator.SandboxDeleteRequest) (*emptypb.Empty, error) {
	ctx, cancel := context.WithTimeoutCause(ctxConn, requestTimeout, fmt.Errorf("request timed out"))
	defer cancel()

	ctx, childSpan := s.tracer.Start(ctx, "sandbox-delete")
	defer childSpan.End()

	childSpan.SetAttributes(
		attribute.String("sandbox.id", in.SandboxId),
		attribute.String("client.id", consul.ClientID),
	)

	sbx, ok := s.sandboxes.Get(in.SandboxId)
	if !ok {
		errMsg := fmt.Errorf("sandbox '%s' not found", in.SandboxId)
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, status.New(codes.NotFound, errMsg.Error()).Err()
	}

	// Don't allow connecting to the sandbox anymore.
	s.dns.Remove(in.SandboxId, sbx.Slot.HostIP())

	// Remove the sandbox from the cache to prevent loading it again in API during the time the instance is stopping.
	// Old comment:
	// 	Ensure the sandbox is removed from cache.
	// 	Ideally we would rely only on the goroutine defer.
	s.sandboxes.Remove(in.SandboxId)

	loggingCtx, cancelLogginCtx := context.WithTimeout(ctx, 2*time.Second)
	defer cancelLogginCtx()

	// Check health metrics before stopping the sandbox
	sbx.Healthcheck(loggingCtx, true)
	sbx.LogMetrics(loggingCtx)

	err := sbx.Stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error stopping sandbox '%s': %v\n", in.SandboxId, err)
	}

	return &emptypb.Empty{}, nil
}

var pauseQueue = semaphore.NewWeighted(maxParalellSnapshotting)

func (s *server) Pause(ctx context.Context, in *orchestrator.SandboxPauseRequest) (*emptypb.Empty, error) {
	ctx, childSpan := s.tracer.Start(ctx, "sandbox-pause")
	defer childSpan.End()

	err := pauseQueue.Acquire(ctx, 1)
	if err != nil {
		telemetry.ReportCriticalError(ctx, err)

		return nil, status.New(codes.ResourceExhausted, err.Error()).Err()
	}

	releaseOnce := sync.OnceFunc(func() {
		pauseQueue.Release(1)
	})

	defer releaseOnce()

	s.pauseMu.Lock()

	sbx, ok := s.sandboxes.Get(in.SandboxId)
	if !ok {
		s.pauseMu.Unlock()

		errMsg := fmt.Errorf("sandbox not found")
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, status.New(codes.NotFound, errMsg.Error()).Err()
	}

	s.dns.Remove(in.SandboxId, sbx.Slot.HostIP())
	s.sandboxes.Remove(in.SandboxId)

	s.pauseMu.Unlock()

	snapshotTemplateFiles, err := storage.NewTemplateFiles(
		in.TemplateId,
		in.BuildId,
		sbx.Config.KernelVersion,
		sbx.Config.FirecrackerVersion,
		sbx.Config.HugePages,
	).NewTemplateCacheFiles()
	if err != nil {
		errMsg := fmt.Errorf("error creating template files: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, status.New(codes.Internal, errMsg.Error()).Err()
	}

	defer func() {
		// sbx.Stop sometimes blocks for several seconds,
		// so we don't want to block the request and do the cleanup in a goroutine after we already removed sandbox from cache and DNS.
		go func() {
			err := sbx.Stop()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error stopping sandbox after snapshot '%s': %v\n", in.SandboxId, err)
			}
		}()
	}()

	err = os.MkdirAll(snapshotTemplateFiles.CacheDir(), 0o755)
	if err != nil {
		errMsg := fmt.Errorf("error creating sandbox cache dir '%s': %w", snapshotTemplateFiles.CacheDir(), err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, status.New(codes.Internal, errMsg.Error()).Err()
	}

	snapshot, err := sbx.Snapshot(ctx, s.tracer, snapshotTemplateFiles, releaseOnce)
	if err != nil {
		errMsg := fmt.Errorf("error snapshotting sandbox '%s': %w", in.SandboxId, err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, status.New(codes.Internal, errMsg.Error()).Err()
	}

	err = s.templateCache.AddSnapshot(
		snapshotTemplateFiles.TemplateId,
		snapshotTemplateFiles.BuildId,
		snapshotTemplateFiles.KernelVersion,
		snapshotTemplateFiles.FirecrackerVersion,
		snapshotTemplateFiles.Hugepages(),
		snapshot.MemfileDiffHeader,
		snapshot.RootfsDiffHeader,
		snapshot.Snapfile,
		snapshot.MemfileDiff,
		snapshot.RootfsDiff,
	)
	if err != nil {
		errMsg := fmt.Errorf("error adding snapshot to template cache: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, status.New(codes.Internal, errMsg.Error()).Err()
	}

	telemetry.ReportEvent(ctx, "added snapshot to template cache")

	go func() {
		var memfilePath *string

		switch r := snapshot.MemfileDiff.(type) {
		case *build.NoDiff:
			break
		default:
			memfileLocalPath, err := r.CachePath()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error getting memfile diff path: %v\n", err)

				return
			}

			memfilePath = &memfileLocalPath
		}

		var rootfsPath *string

		switch r := snapshot.RootfsDiff.(type) {
		case *build.NoDiff:
			break
		default:
			rootfsLocalPath, err := r.CachePath()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error getting rootfs diff path: %v\n", err)

				return
			}

			rootfsPath = &rootfsLocalPath
		}

		b := storage.NewTemplateBuild(
			snapshot.MemfileDiffHeader,
			snapshot.RootfsDiffHeader,
			snapshotTemplateFiles.TemplateFiles,
		)

		err = <-b.Upload(
			context.Background(),
			snapshotTemplateFiles.CacheSnapfilePath(),
			memfilePath,
			rootfsPath,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error uploading sandbox snapshot '%s': %v\n", in.SandboxId, err)

			return
		}
	}()

	return &emptypb.Empty{}, nil
}
