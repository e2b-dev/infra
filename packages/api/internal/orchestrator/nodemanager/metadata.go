package nodemanager

import (
	"context"
	"strconv"

	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/edge"
	grpcshared "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sandboxroutingcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
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

func (n *Node) GetSandboxCreateCtx(ctx context.Context, req *orchestrator.SandboxCreateRequest) (*clusters.GRPCClient, context.Context) {
	md := metadata.MD{}

	if !n.IsNomadManaged() {
		var keepalive *sandboxroutingcatalog.Keepalive
		if keepaliveConfig := req.GetSandbox().GetKeepalive(); keepaliveConfig != nil {
			keepalive = &sandboxroutingcatalog.Keepalive{}
			if traffic := keepaliveConfig.GetTraffic(); traffic != nil && traffic.GetEnabled() {
				keepalive.Traffic = &sandboxroutingcatalog.TrafficKeepalive{
					Enabled: true,
				}
			}
		}

		md = edge.SerializeSandboxCatalogCreateEvent(
			edge.SandboxCatalogCreateEvent{
				SandboxID:               req.GetSandbox().GetSandboxId(),
				TeamID:                  req.GetSandbox().GetTeamId(),
				SandboxMaxLengthInHours: req.GetSandbox().GetMaxSandboxLength(),
				SandboxStartTime:        req.GetStartTime().AsTime(),
				Keepalive:               keepalive,

				ExecutionID:    req.GetSandbox().GetExecutionId(),
				OrchestratorID: n.Metadata().ServiceInstanceID,
				OrchestratorIP: n.IPAddress,
			},
		)
	}

	// Pass snapshot (is_resume) via metadata so the server-side stats handler
	// can include it in otelgrpc metric attributes during TagRPC.
	md.Set(grpcshared.IsResumeMetadataKey, strconv.FormatBool(req.GetSandbox().GetSnapshot()))

	// Merge medata from client (auth, routing with service instance id) and event metadata.
	return n.client, appendMetadataCtx(ctx, md)
}

func (n *Node) GetSandboxDeleteCtx(ctx context.Context, sandboxID string, executionID string) (*clusters.GRPCClient, context.Context) {
	md := metadata.MD{}

	if !n.IsNomadManaged() {
		md = edge.SerializeSandboxCatalogDeleteEvent(
			edge.SandboxCatalogDeleteEvent{
				SandboxID:   sandboxID,
				ExecutionID: executionID,
			},
		)
	}

	// Merge medata from client (auth, routing with service instance id) and event metadata.
	return n.client, appendMetadataCtx(ctx, md)
}

func appendMetadataCtx(ctx context.Context, md metadata.MD) context.Context {
	args := make([]string, 0, len(md)*2)
	for k, v := range md {
		args = append(args, k, v[0])
	}

	return metadata.AppendToOutgoingContext(ctx, args...)
}
