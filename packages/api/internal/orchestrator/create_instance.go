package orchestrator

import (
	"context"
	_ "embed"
	"fmt"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/connectivity"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) CreateSandbox(
	t trace.Tracer,
	ctx context.Context,
	sandboxID,
	templateID,
	alias string,
	teamID uuid.UUID,
	buildID uuid.UUID,
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

	var node *Node
	var excludedNodes []string
	for {
		node, err = o.getLeastBusyNode(childCtx, t, excludedNodes...)
		if err != nil {
			return nil, fmt.Errorf("failed to get least busy node: %w", err)
		}

		telemetry.ReportEvent(childCtx, "Trying to place sandbox on node")

		_, err = node.Client.Sandbox.Create(ctx, &orchestrator.SandboxCreateRequest{
			Sandbox: &orchestrator.SandboxConfig{
				TemplateID:         templateID,
				Alias:              &alias,
				TeamID:             teamID.String(),
				BuildID:            buildID.String(),
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
			if node.Client.connection.GetState() != connectivity.Ready {
				telemetry.ReportEvent(childCtx, "Placing sandbox on node failed, node not ready", attribute.String("node.id", node.ID))
				excludedNodes = append(excludedNodes, node.ID)
			} else {
				return nil, fmt.Errorf("failed to create sandbox '%s': %w", templateID, err)
			}
		}

		break
	}

	telemetry.SetAttributes(childCtx, attribute.String("node.id", node.ID))
	telemetry.ReportEvent(childCtx, "Created sandbox")

	sbx := &api.Sandbox{
		ClientID:   node.ID,
		SandboxID:  sandboxID,
		TemplateID: templateID,
		Alias:      &alias,
	}

	if cacheErr := o.instanceCache.Add(instance.InstanceInfo{
		StartTime:         nil,
		Instance:          sbx,
		BuildID:           &buildID,
		TeamID:            &teamID,
		Metadata:          metadata,
		MaxInstanceLength: time.Duration(maxInstanceLengthHours) * time.Hour,
		VCPU:              vCPU,
		RamMB:             ramMB,
	}); cacheErr != nil {
		errMsg := fmt.Errorf("error when adding instance to cache: %w", cacheErr)
		telemetry.ReportError(ctx, errMsg)

		delErr := o.DeleteInstance(ctx, sandboxID)
		if delErr != nil {
			delErrMsg := fmt.Errorf("couldn't delete instance that couldn't be added to cache: %w", delErr)
			telemetry.ReportError(ctx, delErrMsg)
		} else {
			telemetry.ReportEvent(ctx, "deleted instance that couldn't be added to cache")
		}

		return nil, fmt.Errorf("cannot create a sandbox right now")
	}

	return sbx, nil
}

func (o *Orchestrator) getLeastBusyNode(ctx context.Context, tracer trace.Tracer, excludedNodes ...string) (*Node, error) {
	childCtx, childSpan := tracer.Start(ctx, "get-least-busy-node")
	defer childSpan.End()

	var leastBusyNode *Node
	for _, node := range o.nodes {
		if leastBusyNode == nil || node.CPUUsage < leastBusyNode.CPUUsage {
			for _, excludedNode := range excludedNodes {
				if node.ID == excludedNode {
					continue
				}
			}
			leastBusyNode = node
		}
	}

	telemetry.ReportEvent(childCtx, "Found least busy node")
	return leastBusyNode, nil
}
