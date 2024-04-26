package server

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/constants"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
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
	)

	sbx, err := sandbox.NewSandbox(
		childCtx,
		s.tracer,
		s.consul,
		s.dns,
		req.Sandbox,
		childSpan.SpanContext().TraceID().String(),
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
		defer sbx.CleanupAfterFCStop(context.Background(), tracer, s.consul, s.dns)

		err := sbx.Wait(context.Background(), tracer)
		if err != nil {
			errMsg := fmt.Errorf("failed to wait for Sandbox: %w", err)
			telemetry.ReportCriticalError(closeCtx, errMsg)
		}

		go func(sandboxID string) { s.failedSandbox <- sandboxID }(req.Sandbox.SandboxID)

		// Wait before removing all resources (see defers above)
		time.Sleep(1 * time.Second)
	}()

	return &orchestrator.SandboxCreateResponse{
		ClientID: constants.ClientID,
	}, nil
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
		})
	}

	return &orchestrator.SandboxListResponse{
		Sandboxes: sandboxes,
	}, nil
}

func (s *server) Delete(ctx context.Context, in *orchestrator.SandboxRequest) (*emptypb.Empty, error) {
	_, childSpan := s.tracer.Start(ctx, "sandbox-delete")
	defer childSpan.End()

	sbx, ok := s.sandboxes.Get(in.SandboxID)
	if !ok {
		errMsg := fmt.Errorf("sandbox not found")
		telemetry.ReportError(ctx, errMsg)

		return nil, status.New(codes.NotFound, errMsg.Error()).Err()
	}

	sbx.Stop(ctx, s.tracer)

	return nil, nil
}

func (s *server) StreamFailedSandbox(_ *emptypb.Empty, stream orchestrator.Sandbox_StreamFailedSandboxServer) error {
	ctx := stream.Context()
	_, childSpan := s.tracer.Start(ctx, "failed-sandbox")
	defer childSpan.End()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sandboxID := <-s.failedSandbox:
			if err := stream.Send(&orchestrator.FailedSandbox{SandboxID: sandboxID}); err != nil {
				return err
			}
		}
	}
}
