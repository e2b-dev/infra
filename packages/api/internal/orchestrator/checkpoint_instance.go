package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/storageopts"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// CheckpointSandbox snapshots a running sandbox in place: the sandbox is
// briefly paused on its node, snapshotted, and resumed under the same
// execution ID, so it keeps running with its ID, expiration, and reservation
// untouched. The snapshot is written to the sandbox's own snapshots row, so
// it can immediately be resumed or forked from.
func (o *Orchestrator) CheckpointSandbox(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	ctx, span := tracer.Start(ctx, "checkpoint-sandbox")
	defer span.End()

	sbx, alreadyDone, finishSnapshotting, err := o.sandboxStore.StartRemoving(ctx, teamID, sandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionSnapshot})
	if err != nil {
		return fmt.Errorf("failed to start snapshotting: %w", err)
	}

	// alreadyDone conflates joining a concurrent checkpoint that just
	// succeeded with finding the sandbox stuck in Snapshotting after a failed
	// one, where no fresh snapshot exists. Treating it as success could fork
	// stale state, so report a conflict and let the caller retry (same
	// behavior as CreateSnapshotTemplate).
	if alreadyDone {
		return &sandbox.InvalidStateTransitionError{
			CurrentState: sandbox.StateSnapshotting,
			TargetState:  sandbox.StateSnapshotting,
		}
	}

	// finish completes the snapshotting transition exactly once.
	// On success (nil) it restores the sandbox to Running.
	// On error it leaves the state as Snapshotting so that
	// RemoveSandbox can transition directly to Killing.
	var once sync.Once
	finish := func(err error) {
		once.Do(func() {
			finishSnapshotting(context.WithoutCancel(ctx), err)
		})
	}
	defer finish(nil)

	node := o.getOrConnectNode(ctx, sbx.ClusterID, sbx.NodeID)
	if node == nil {
		return fmt.Errorf("node '%s' not found", sbx.NodeID)
	}

	upsertResult, err := o.throttledUpsertSnapshot(ctx, buildUpsertSnapshotParams(sbx, node, false))
	if err != nil {
		return fmt.Errorf("error upserting snapshot: %w", err)
	}

	// Checkpoint pauses the sandbox, snapshots it, and resumes it on the
	// orchestrator with the same ExecutionID. On error the orchestrator
	// kills the sandbox itself; RemoveSandbox is still needed to clean up
	// API-side state (store, routing, analytics).
	client, childCtx := node.GetClient(ctx)
	_, err = client.Sandbox.Checkpoint(childCtx, &orchestrator.SandboxCheckpointRequest{
		SandboxId: sbx.SandboxID,
		BuildId:   upsertResult.BuildID.String(),
		Metadata:  map[string]string{storageopts.ObjectMetadataTemplateID: upsertResult.TemplateID},
	})
	if err != nil {
		o.failSnapshotBuild(ctx, upsertResult.BuildID, err)

		// Complete the snapshotting transition with error — leaves state as
		// Snapshotting (no restore to Running) and clears the transition key
		// so RemoveSandbox can proceed without deadlock.
		finish(err)

		if killErr := o.RemoveSandbox(ctx, teamID, sandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill}); killErr != nil {
			telemetry.ReportError(ctx, "error killing sandbox after failed checkpoint", killErr)
		}

		return fmt.Errorf("checkpoint failed: %w", err)
	}

	now := time.Now()
	err = o.sqlcDB.UpdateEnvBuildStatus(ctx, queries.UpdateEnvBuildStatusParams{
		Status:     types.BuildStatusSuccess,
		FinishedAt: &now,
		Reason:     types.BuildReason{},
		BuildID:    upsertResult.BuildID,
	})
	if err != nil {
		return fmt.Errorf("error updating build status: %w", err)
	}

	o.snapshotCache.Invalidate(context.WithoutCancel(ctx), sandboxID)

	telemetry.ReportEvent(ctx, "Checkpointed sandbox")

	return nil
}
