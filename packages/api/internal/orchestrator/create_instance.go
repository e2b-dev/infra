package orchestrator

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/orchestration"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) CreateSandbox(
	t trace.Tracer,
	ctx context.Context,
	sandboxID,
	templateID,
	alias,
	teamID,
	buildID string,
	maxInstanceLengthHours int64,
	metadata map[string]string,
	kernelVersion,
	firecrackerVersion string,
	vCPU int64,
	ramMB int64,
) (*api.Sandbox, error) {
	childCtx, childSpan := t.Start(ctx, "create-sandbox",
		trace.WithAttributes(
			attribute.String("env.id", templateID),
		),
	)
	defer childSpan.End()

	features, err := sandbox.NewVersionInfo(firecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", firecrackerVersion, err)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "Got FC version info")

	nodeID, err := o.getLeastBusyNode(childCtx, t)
	if err != nil {
		return nil, fmt.Errorf("failed to get least busy node: %w", err)
	}

	telemetry.SetAttributes(childCtx, attribute.String("node.id", nodeID))
	telemetry.ReportEvent(childCtx, "Placing sandbox on node")

	client, err := o.GetClientByNodeID(nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get GRPC client: %w", err)
	}

	telemetry.ReportEvent(childCtx, "Got GRPC client")

	_, err = client.Sandbox.Create(ctx, &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			TemplateID:         templateID,
			Alias:              &alias,
			TeamID:             teamID,
			BuildID:            buildID,
			SandboxID:          sandboxID,
			KernelVersion:      kernelVersion,
			FirecrackerVersion: firecrackerVersion,
			Metadata:           metadata,
			MaxInstanceLength:  maxInstanceLengthHours,
			HugePages:          features.HasHugePages(),
			VCpu:               vCPU,
			RamMB:              ramMB,
		},
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox '%s': %w", templateID, err)
	}

	telemetry.ReportEvent(childCtx, "Created sandbox")

	return &api.Sandbox{
		ClientID:   nodeID,
		SandboxID:  sandboxID,
		TemplateID: templateID,
		Alias:      &alias,
	}, nil
}

type Node struct {
	ID       string
	CPUUsage int64
	RamUsage int64
}

func (o *Orchestrator) getLeastBusyNode(ctx context.Context, tracer trace.Tracer) (string, error) {
	childCtx, childSpan := tracer.Start(ctx, "get-least-busy-node")
	defer childSpan.End()

	nodesInfo, err := o.ListNodes()
	if err != nil {
		errMsg := fmt.Errorf("failed to get nodes: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return "", errMsg
	}
	telemetry.ReportEvent(childCtx, "Got list of nodes ")

	if len(nodesInfo) == 0 {
		errMsg := fmt.Errorf("no nodes found")
		telemetry.ReportCriticalError(childCtx, errMsg)

		return "", errMsg
	}

	nodes := make([]*Node, 0, len(nodesInfo))
	for _, nodeInfo := range nodesInfo {
		node := &Node{ID: o.getIdFromNode(nodeInfo)}
		key := orchestration.GetKVSandboxDataPrefix(node.ID)

		sandboxes, _, err := o.consulClient.KV().List(key, nil)
		if err != nil {
			errMsg := fmt.Errorf("failed to get sandbox info from Consul: %w", err)
			telemetry.ReportCriticalError(childCtx, errMsg)

			return "", errMsg
		}

		for _, s := range sandboxes {
			var sandboxInfo = &orchestration.Info{}
			if err := json.Unmarshal(s.Value, sandboxInfo); err != nil {
				errMsg := fmt.Errorf("failed to unmarshal sandbox info: %w", err)
				telemetry.ReportCriticalError(childCtx, errMsg)

				return "", errMsg
			}
			node.CPUUsage += sandboxInfo.Config.VCpu
			node.RamUsage += sandboxInfo.Config.RamMB
		}
		nodes = append(nodes, node)
	}

	telemetry.ReportEvent(childCtx, "Calculated CPU and RAM usage for nodes")

	// TODO: implement a better algorithm for choosing the least busy node
	leastBusyNode := nodes[0]
	for _, node := range nodes {
		if node.CPUUsage < leastBusyNode.CPUUsage {
			leastBusyNode = node
		}
	}

	telemetry.ReportEvent(childCtx, "Found least busy node")
	return leastBusyNode.ID, nil
}
