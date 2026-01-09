package edgepassthrough

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

func (s *NodePassThroughServer) eventsHandler(ctx context.Context, md metadata.MD) (func(error), error) {
	eventTypeHeaders := md.Get(edge.EventTypeHeader)
	if len(eventTypeHeaders) == 0 {
		return nil, nil
	}

	if len(eventTypeHeaders) > 1 {
		return nil, status.Errorf(codes.InvalidArgument, "multiple event types are not supported: %v", eventTypeHeaders)
	}

	eventType := eventTypeHeaders[0]
	switch eventType {
	case edge.CatalogCreateEventType:
		return s.catalogCreateEventHandler(ctx, md)
	case edge.CatalogDeleteEventType:
		return s.catalogDeleteEventHandler(ctx, md)
	default:
		return nil, status.Errorf(codes.InvalidArgument, "event type %s is not supported", eventType)
	}
}

func (s *NodePassThroughServer) catalogCreateEventHandler(ctx context.Context, md metadata.MD) (func(error), error) {
	c, err := edge.ParseSandboxCatalogCreateEvent(md)
	if err != nil {
		return nil, err
	}

	o, ok := s.nodes.GetOrchestrator(c.OrchestratorID)
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "orchestrator %s not found", c.OrchestratorID)
	}

	err = s.catalog.StoreSandbox(
		ctx,
		c.SandboxID,
		&catalog.SandboxInfo{
			OrchestratorID: c.OrchestratorID,
			OrchestratorIP: o.GetInfo().IP,

			ExecutionID:      c.ExecutionID,
			StartedAt:        c.SandboxStartTime,
			MaxLengthInHours: c.SandboxMaxLengthInHours,
		},
		time.Duration(c.SandboxMaxLengthInHours)*time.Hour,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to store sandbox in catalog: %w", err)
	}

	return func(err error) {
		if err == nil {
			return
		}

		ctx := context.WithoutCancel(ctx)
		deleteErr := s.catalog.DeleteSandbox(ctx, c.SandboxID, c.ExecutionID)
		if deleteErr != nil {
			logger.L().Error(ctx, "Failed to delete sandbox in catalog after failing request", zap.Error(deleteErr))
		}
	}, nil
}

func (s *NodePassThroughServer) catalogDeleteEventHandler(ctx context.Context, md metadata.MD) (func(error), error) {
	d, err := edge.ParseSandboxCatalogDeleteEvent(md)
	if err != nil {
		return nil, err
	}

	err = s.catalog.DeleteSandbox(ctx, d.SandboxID, d.ExecutionID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete sandbox from catalog: %w", err)
	}

	return nil, nil
}
