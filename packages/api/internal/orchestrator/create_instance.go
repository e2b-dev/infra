package orchestrator

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	consulapi "github.com/hashicorp/consul/api"
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
	consulClient *consulapi.Client,
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

	nodeID, err := o.getLeastBusyNode(childCtx, t, consulClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get least busy node: %w", err)
	}

	host, err := o.GetHost(nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get host: %w", err)
	}

	client, err := o.GetClient(host)
	if err != nil {
		return nil, fmt.Errorf("failed to get GRPC client: %w", err)
	}

	res, err := client.Sandbox.Create(ctx, &orchestrator.SandboxCreateRequest{
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
		ClientID:   res.ClientID,
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

func (o *Orchestrator) getLeastBusyNode(ctx context.Context, tracer trace.Tracer, consulClient *consulapi.Client) (string, error) {
	childCtx, childSpan := tracer.Start(ctx, "get-least-busy-node")
	defer childSpan.End()

	nodesInfo, _, err := consulClient.Catalog().Nodes(nil)
	if err != nil {
		errMsg := fmt.Errorf("failed to get nodes from Consul: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return "", errMsg
	}
	telemetry.ReportEvent(childCtx, "Got nodes from Consul")

	if len(nodesInfo) == 0 {
		errMsg := fmt.Errorf("no nodes found in Consul")
		telemetry.ReportCriticalError(childCtx, errMsg)

		return "", errMsg
	}

	nodes := make([]*Node, 0, len(nodesInfo))
	for _, nodeInfo := range nodesInfo {
		node := &Node{
			ID: o.getIdFromNode(nodeInfo),
		}
		key := orchestration.GetKVSandboxDataPrefix(node.ID)
		sandboxes, _, err := consulClient.KV().List(key, nil)
		if err != nil {
			errMsg := fmt.Errorf("failed to get sandboxes from Consul: %w", err)
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

	// TODO: implement a better algorithm for choosing the least busy node
	leastBusyNode := nodes[0]
	for _, node := range nodes {
		if node.CPUUsage < leastBusyNode.CPUUsage {
			leastBusyNode = node
		}
	}

	// TODO: use function to get the node ID
	return nodes[0].ID, nil
}
