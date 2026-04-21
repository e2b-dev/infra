package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// PatchSandboxMetadata applies a JSON-Merge-Patch-style update: non-nil values
// upsert the key, a nil pointer (or empty string) removes it, and absent keys
// are left alone.
func (o *Orchestrator) PatchSandboxMetadata(
	ctx context.Context,
	teamID uuid.UUID,
	sandboxID string,
	patch map[string]*string,
) *api.APIError {
	var merged map[string]string

	updateFunc := func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		if sbx.State != sandbox.StateRunning {
			return sbx, &sandbox.NotRunningError{SandboxID: sandboxID, State: sbx.State}
		}

		merged = applyMetadataPatch(sbx.Metadata, patch)
		sbx.Metadata = merged

		return sbx, nil
	}

	var sbxNotRunningErr *sandbox.NotRunningError

	sbx, err := o.sandboxStore.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		switch {
		case errors.As(err, &sbxNotRunningErr):
			return &api.APIError{Code: http.StatusConflict, ClientMsg: utils.SandboxChangingStateMsg(sandboxID, sbxNotRunningErr.State), Err: err}
		case errors.Is(err, sandbox.ErrNotFound):
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: utils.SandboxNotFoundMsg(sandboxID), Err: err}
		default:
			return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error patching sandbox metadata", Err: err}
		}
	}

	return o.patchSandboxMetadataOnNode(ctx, sbx, merged)
}

func applyMetadataPatch(current map[string]string, patch map[string]*string) map[string]string {
	out := make(map[string]string, len(current)+len(patch))
	maps.Copy(out, current)
	for k, v := range patch {
		if v == nil || *v == "" {
			delete(out, k)
		} else {
			out[k] = *v
		}
	}

	return out
}

func (o *Orchestrator) patchSandboxMetadataOnNode(
	ctx context.Context,
	sbx sandbox.Sandbox,
	metadata map[string]string,
) *api.APIError {
	ctx, span := tracer.Start(ctx, "patch-sandbox-metadata-on-node",
		trace.WithAttributes(
			attribute.String("instance.id", sbx.SandboxID),
		),
	)
	defer span.End()

	node := o.getOrConnectNode(ctx, sbx.ClusterID, sbx.NodeID)
	if node == nil {
		return &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: fmt.Sprintf("Node hosting sandbox '%s' not found", sbx.SandboxID),
			Err:       fmt.Errorf("node '%s' not found for cluster '%s'", sbx.NodeID, sbx.ClusterID),
		}
	}

	client, ctx := node.GetClient(ctx)
	_, err := client.Sandbox.Update(ctx, &orchestratorgrpc.SandboxUpdateRequest{
		SandboxId: sbx.SandboxID,
		Metadata:  &orchestratorgrpc.SandboxMetadataUpdate{Entries: metadata},
	})
	if err != nil {
		grpcErr, ok := status.FromError(err)
		if ok && grpcErr.Code() == codes.NotFound {
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: utils.SandboxNotFoundMsg(sbx.SandboxID), Err: err}
		}

		err = utils.UnwrapGRPCError(err)
		telemetry.ReportCriticalError(ctx, "failed to patch sandbox metadata on node", err)

		return &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Error applying metadata to sandbox",
			Err:       fmt.Errorf("failed to patch sandbox metadata on node: %w", err),
		}
	}

	telemetry.ReportEvent(ctx, "Patched sandbox metadata on node")

	return nil
}
