package orchestrator

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

type RemoveType string

const (
	RemoveTypePause RemoveType = "pause"
	RemoveTypeKill  RemoveType = "kill"
)

func (o *Orchestrator) RemoveInstance(ctx context.Context, sandbox *instance.InstanceInfo, removeType RemoveType) error {
	_, childSpan := o.tracer.Start(ctx, "remove-instance")
	defer childSpan.End()

	// Mark the sandbox as being removed to prevent it from being evicted by the cache or another call
	err := o.instanceCache.StartRemoving(sandbox.SandboxID)
	if err != nil {
		return fmt.Errorf("failed to start removing sandbox '%s': %w", sandbox.SandboxID, err)
	}

	defer o.instanceCache.Remove(sandbox.SandboxID)

	return o.removeInstance(ctx, sandbox, removeType)
}

// removeInstance should be called from places where you already marked the sandbox as being removed
func (o *Orchestrator) removeInstance(ctx context.Context, sandbox *instance.InstanceInfo, removeType RemoveType) error {
	duration := time.Since(sandbox.StartTime).Seconds()
	stopTime := time.Now()

	// Run in separate goroutine to not block sandbox deletion
	go reportInstanceStopAnalytics(
		context.WithoutCancel(ctx),
		o.posthogClient,
		o.analytics,
		sandbox.TeamID.String(),
		sandbox.SandboxID,
		sandbox.ExecutionID,
		sandbox.TemplateID,
		sandbox.VCpu,
		sandbox.RamMB,
		sandbox.TotalDiskSizeMB,
		stopTime,
		removeType,
		duration,
	)

	node := o.GetNode(sandbox.ClusterID, sandbox.NodeID)
	if node == nil {
		zap.L().Error("failed to get node", logger.WithNodeID(sandbox.NodeID))
		return fmt.Errorf("node '%s' not found", sandbox.NodeID)
	}

	// Remove the sandbox resources after the sandbox is deleted
	defer node.RemoveSandbox(sandbox)

	o.dns.Remove(ctx, sandbox.SandboxID, node.IPAddress)

	sbxlogger.I(sandbox).Debug("Removing sandbox",
		zap.Bool("auto_pause", sandbox.AutoPause),
		zap.String("remove_type", string(removeType)),
	)

	switch removeType {
	case RemoveTypePause:
		err := o.pauseSandbox(ctx, node, sandbox)
		if err != nil {
			return fmt.Errorf("failed to auto pause sandbox '%s': %w", sandbox.SandboxID, err)
		}
	case RemoveTypeKill:
		// Set the sandbox as expired to prevent it from being evicted
		sandbox.SetExpired()

		req := &orchestrator.SandboxDeleteRequest{SandboxId: sandbox.SandboxID}
		client, ctx := node.GetClient(ctx)
		_, err := client.Sandbox.Delete(node.GetSandboxDeleteCtx(ctx, sandbox.SandboxID, sandbox.ExecutionID), req)
		if err != nil {
			return fmt.Errorf("failed to delete sandbox '%s': %w", sandbox.SandboxID, err)
		}
	}

	return nil
}
