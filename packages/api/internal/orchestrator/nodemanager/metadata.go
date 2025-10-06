package nodemanager

import (
	"context"

	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type NodeMetadata struct {
	// Service instance ID is unique identifier for every orchestrator process, after restart it will change.
	// In the future, we want to migrate to using this ID instead of node ID for tracking orchestrators-
	ServiceInstanceID string

	Commit  string
	Version string
}

func (n *Node) setMetadata(md NodeMetadata) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	n.meta = md
}

func (n *Node) Metadata() NodeMetadata {
	n.mutex.RLock()
	defer n.mutex.RUnlock()
	return n.meta
}

// Generates Metadata with the current service instance ID
// to ensure we always use the latest ID (e.g. after orchestrator restarts)
func (n *Node) getClientMetadata() metadata.MD {
	return metadata.New(map[string]string{consts.EdgeRpcServiceInstanceIDHeader: n.Metadata().ServiceInstanceID})
}

func (n *Node) GetSandboxCreateCtx(ctx context.Context, req *orchestrator.SandboxCreateRequest) context.Context {
	// Skip local cluster. It should be okay to send it here, but we don't want to do it until we explicitly support it.
	if n.IsNomadManaged() {
		return ctx
	}

	md := edge.SerializeSandboxCatalogCreateEvent(
		edge.SandboxCatalogCreateEvent{
			SandboxID:               req.GetSandbox().GetSandboxId(),
			SandboxMaxLengthInHours: req.GetSandbox().GetMaxSandboxLength(),
			SandboxStartTime:        req.GetStartTime().AsTime(),

			ExecutionID:    req.GetSandbox().GetExecutionId(),
			OrchestratorID: n.Metadata().ServiceInstanceID,
		},
	)

	return metadata.NewOutgoingContext(ctx, metadata.Join(n.getClientMetadata(), md))
}

func (n *Node) GetSandboxDeleteCtx(ctx context.Context, sandboxID string, executionID string) context.Context {
	// Skip local cluster. It should be okay to send it here, but we don't want to do it until we explicitly support it.
	if n.IsNomadManaged() {
		return ctx
	}

	md := edge.SerializeSandboxCatalogDeleteEvent(
		edge.SandboxCatalogDeleteEvent{
			SandboxID:   sandboxID,
			ExecutionID: executionID,
		},
	)

	return metadata.NewOutgoingContext(ctx, metadata.Join(n.getClientMetadata(), md))
}
