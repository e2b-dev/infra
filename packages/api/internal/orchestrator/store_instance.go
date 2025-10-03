package orchestrator

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) CopySandboxToBucket(
	ctx context.Context,
	sandboxID string,
	clusterID uuid.UUID,
	nodeID string,
) (*orchestrator.DiffStoreConfig, error) {
	childCtx, childSpan := tracer.Start(ctx, "save-sandbox-to-store",
		trace.WithAttributes(
			attribute.String("instance.id", sandboxID),
		),
	)
	defer childSpan.End()

	client, childCtx, err := o.GetClient(childCtx, clusterID, nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get client '%s': %w", nodeID, err)
	}

	buildID := uuid.New().String()

	storeConfig := &orchestrator.DiffStoreConfig{
		Store: &orchestrator.DiffStoreConfig_Bucket_{
			Bucket: &orchestrator.DiffStoreConfig_Bucket{
				Name: "fc-copy-cache",
				Path: fmt.Sprintf("%s/%s", sandboxID, buildID),
			},
		},
	}

	_, err = client.Sandbox.CopyToStore(
		childCtx, &orchestrator.SandboxCopyRequest{
			SandboxId: sandboxID,
			BuildId:   buildID,
			Store:     storeConfig,
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to update sandbox '%s': %w", sandboxID, err)
	}

	telemetry.ReportEvent(childCtx, "Saved sandbox to store")

	return storeConfig, nil
}
