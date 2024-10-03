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

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *server) Create(ctx context.Context, req *orchestrator.SandboxCreateRequest) (*orchestrator.SandboxCreateResponse, error) {
	childCtx, childSpan := s.tracer.Start(ctx, "sandbox-create")

	defer childSpan.End()
	childSpan.SetAttributes(
		attribute.String("sandbox.template.id", req.Sandbox.TemplateId),
		attribute.String("sandbox.kernel.version", req.Sandbox.KernelVersion),
		attribute.String("sandbox.id", req.Sandbox.SandboxId),
		attribute.String("client.id", consul.ClientID),
		attribute.String("sandbox.envd.version", req.Sandbox.EnvdVersion),
	)

	sbx, err := sandbox.NewSandbox(
		childCtx,
		s.tracer,
		s.consul,
		s.dns,
		s.networkPool,
		s.templateCache,
		s.nbdPool,
		req.Sandbox,
		childSpan.SpanContext().TraceID().String(),
		req.StartTime.AsTime(),
		req.EndTime.AsTime(),
	)
	if err != nil {
		errMsg := fmt.Errorf("failed to create sandbox: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, status.New(codes.Internal, errMsg.Error()).Err()
	}

	s.sandboxes.Insert(req.Sandbox.SandboxId, sbx)

	go func() {
		tracer := otel.Tracer("close")
		closeCtx, _ := tracer.Start(ctx, "close-sandbox")

		defer telemetry.ReportEvent(closeCtx, "sandbox closed")
		defer s.sandboxes.Remove(req.Sandbox.SandboxId)
		defer sbx.CleanupAfterFCStop(context.Background(), tracer, s.consul, s.dns, req.Sandbox.SandboxId)

		waitErr := sbx.Wait(context.Background(), tracer)
		if waitErr != nil {
			errMsg := fmt.Errorf("failed to wait for Sandbox: %w", waitErr)
			fmt.Println(errMsg)
		} else {
			fmt.Printf("Sandbox %s wait finished\n", req.Sandbox.SandboxId)
		}
	}()

	return &orchestrator.SandboxCreateResponse{
		ClientId: consul.ClientID,
	}, nil
}

func (s *server) Update(ctx context.Context, req *orchestrator.SandboxUpdateRequest) (*emptypb.Empty, error) {
	_, childSpan := s.tracer.Start(ctx, "sandbox-update")
	defer childSpan.End()

	item, ok := s.sandboxes.Get(req.SandboxId)
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

func (s *server) Delete(ctx context.Context, in *orchestrator.SandboxDeleteRequest) (*emptypb.Empty, error) {
	_, childSpan := s.tracer.Start(ctx, "sandbox-delete")
	defer childSpan.End()
	childSpan.SetAttributes(
		attribute.String("sandbox.id", in.SandboxId),
		attribute.String("client.id", consul.ClientID),
	)

	sbx, ok := s.sandboxes.Get(in.SandboxId)
	if !ok {
		errMsg := fmt.Errorf("sandbox not found")
		telemetry.ReportError(ctx, errMsg)

		return nil, status.New(codes.NotFound, errMsg.Error()).Err()
	}

	childSpan.SetAttributes(
		attribute.String("sandbox.template.id", sbx.Config.TemplateId),
		attribute.String("sandbox.kernel.version", sbx.Config.KernelVersion),
	)

	// Don't allow connecting to the sandbox anymore.
	s.dns.Remove(in.SandboxId)

	sbx.Stop(ctx, s.tracer)

	// Ensure the sandbox is removed from cache.
	// Ideally we would rely only on the goroutine defer.
	s.sandboxes.Remove(in.SandboxId)

	return nil, nil
}
