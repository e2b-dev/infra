package server

import (
	"context"
	"fmt"
	"os"

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

	telemetry.SetAttributes(
		childCtx,
		attribute.String("client.id", consul.ClientID),
		attribute.String("sandbox.id", req.Sandbox.SandboxId),
		attribute.String("sandbox.template.id", req.Sandbox.TemplateId),
		attribute.String("sandbox.envd.version", req.Sandbox.EnvdVersion),
		attribute.String("sandbox.kernel.version", req.Sandbox.KernelVersion),
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
		defer s.sandboxes.Remove(req.Sandbox.SandboxId)
		defer sbx.Cleanup(s.consul, s.dns, req.Sandbox.SandboxId)

		waitErr := sbx.Wait()
		if waitErr != nil {
			fmt.Fprintf(os.Stderr, "[sandbox %s]: failed to wait for Sandbox: %v\n", req.Sandbox.SandboxId, waitErr)
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
	childCtx, childSpan := s.tracer.Start(ctx, "sandbox-delete")
	defer childSpan.End()

	telemetry.SetAttributes(
		childCtx,
		attribute.String("client.id", consul.ClientID),
		attribute.String("sandbox.id", in.SandboxId),
	)

	sbx, ok := s.sandboxes.Get(in.SandboxId)
	if !ok {
		errMsg := fmt.Errorf("sandbox not found")

		return nil, status.New(codes.NotFound, errMsg.Error()).Err()
	}

	// Don't allow connecting to the sandbox anymore.
	s.dns.Remove(in.SandboxId)

	sbx.Stop()

	// Ensure the sandbox is removed from cache.
	// Ideally we would rely only on the goroutine defer.
	s.sandboxes.Remove(in.SandboxId)

	return &emptypb.Empty{}, nil
}
