package server

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/constants"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *server) Create(ctx context.Context, req *orchestrator.SandboxCreateRequest) (*orchestrator.SandboxCreateResponse, error) {
	childCtx, childSpan := s.tracer.Start(ctx, "sandbox-create")

	defer childSpan.End()
	childSpan.SetAttributes(
		attribute.String("env.id", req.Sandbox.TemplateID),
		attribute.String("env.kernel.version", req.Sandbox.KernelVersion),
		attribute.String("instance.id", req.Sandbox.SandboxID),
		attribute.String("client.id", constants.ClientID),
		attribute.String("envd.version", req.Sandbox.EnvdVersion),
	)

	logger := logs.NewSandboxLogger(
		req.Sandbox.SandboxID,
		req.Sandbox.TemplateID,
		req.Sandbox.TeamID,
		req.Sandbox.VCpuCount,
		req.Sandbox.MemoryMB,
		false,
	)

	sbx, err := sandbox.NewSandbox(
		childCtx,
		s.tracer,
		s.consul,
		s.dns,
		s.networkPool,
		req.Sandbox,
		childSpan.SpanContext().TraceID().String(),
		req.StartTime.AsTime(),
		req.EndTime.AsTime(),
		logger,
	)
	if err != nil {
		errMsg := fmt.Errorf("failed to create sandbox: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, status.New(codes.Internal, errMsg.Error()).Err()
	}

	s.sandboxes.Insert(req.Sandbox.SandboxID, sbx)

	go func() {
		tracer := otel.Tracer("close")
		closeCtx, _ := tracer.Start(ctx, "close-sandbox")

		defer telemetry.ReportEvent(closeCtx, "sandbox closed")
		defer s.sandboxes.Remove(req.Sandbox.SandboxID)
		defer sbx.CleanupAfterFCStop(context.Background(), tracer, s.consul, s.dns, req.Sandbox.SandboxID)

		waitErr := sbx.Wait(context.Background(), tracer)
		if waitErr != nil {
			errMsg := fmt.Errorf("failed to wait for Sandbox: %w", waitErr)
			fmt.Println(errMsg)
		} else {
			fmt.Printf("Sandbox %s wait finished\n", req.Sandbox.SandboxID)
		}
		logger.Infof("Sandbox killed")
	}()

	return &orchestrator.SandboxCreateResponse{
		ClientID: constants.ClientID,
	}, nil
}

func (s *server) Update(ctx context.Context, req *orchestrator.SandboxUpdateRequest) (*emptypb.Empty, error) {
	_, childSpan := s.tracer.Start(ctx, "sandbox-update")
	defer childSpan.End()

	item, ok := s.sandboxes.Get(req.SandboxID)
	if !ok {
		errMsg := fmt.Errorf("sandbox not found")
		telemetry.ReportError(ctx, errMsg)

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

		if sbx.Sandbox == nil {
			continue
		}

		sandboxes = append(sandboxes, &orchestrator.RunningSandbox{
			Config:    sbx.Sandbox,
			ClientID:  constants.ClientID,
			StartTime: timestamppb.New(sbx.StartedAt),
			EndTime:   timestamppb.New(sbx.EndAt),
		})
	}

	return &orchestrator.SandboxListResponse{
		Sandboxes: sandboxes,
	}, nil
}

func (s *server) Delete(ctx context.Context, in *orchestrator.SandboxRequest) (*emptypb.Empty, error) {
	_, childSpan := s.tracer.Start(ctx, "sandbox-delete")
	defer childSpan.End()
	childSpan.SetAttributes(
		attribute.String("instance.id", in.SandboxID),
		attribute.String("client.id", constants.ClientID),
	)

	sbx, ok := s.sandboxes.Get(in.SandboxID)
	if !ok {
		errMsg := fmt.Errorf("sandbox not found")
		telemetry.ReportError(ctx, errMsg)

		return nil, status.New(codes.NotFound, errMsg.Error()).Err()
	}

	sbx.Healthcheck(ctx, true)

	childSpan.SetAttributes(
		attribute.String("env.id", sbx.Sandbox.TemplateID),
		attribute.String("env.kernel.version", sbx.Sandbox.KernelVersion),
	)

	// Don't allow connecting to the sandbox anymore.
	s.dns.Remove(in.SandboxID)

	sbx.Stop(ctx, s.tracer)

	// Ensure the sandbox is removed from cache.
	// Ideally we would rely only on the goroutine defer.
	s.sandboxes.Remove(in.SandboxID)

	return &emptypb.Empty{}, nil
}
