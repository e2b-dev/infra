package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type SnapshotTemplateResult struct {
	TemplateID string
	BuildID    uuid.UUID
}

type SnapshotTemplateOpts struct {
	// ExistingTemplateID is set when the alias resolved to an existing template owned by the team.
	ExistingTemplateID *string
	// Alias is the parsed alias name (without namespace). Set when a name was provided.
	Alias *string
	// Namespace is the team slug used for alias scoping. Set when a name was provided.
	Namespace *string
	// Tag is the build tag parsed from the name, defaults to "default".
	Tag string
}

// CreateSnapshotTemplate creates a persistent snapshot template from a running sandbox and immediately resumes it.
// The handler is responsible for parsing the name, resolving aliases via the cache, and populating opts.
func (o *Orchestrator) CreateSnapshotTemplate(ctx context.Context, teamID uuid.UUID, sandboxID string, opts SnapshotTemplateOpts) (SnapshotTemplateResult, error) {
	ctx, span := tracer.Start(ctx, "create-snapshot-template")
	defer span.End()

	sbx, alreadyDone, finishSnapshotting, err := o.sandboxStore.StartRemoving(ctx, teamID, sandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionSnapshot})
	if err != nil {
		return SnapshotTemplateResult{}, fmt.Errorf("failed to start snapshotting: %w", err)
	}

	if alreadyDone {
		return SnapshotTemplateResult{}, &sandbox.InvalidStateTransitionError{
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
		return SnapshotTemplateResult{}, fmt.Errorf("node '%s' not found", sbx.NodeID)
	}

	upsertResult, err := o.throttledUpsertSnapshot(ctx, buildUpsertSnapshotParams(sbx, node))
	if err != nil {
		return SnapshotTemplateResult{}, fmt.Errorf("error upserting snapshot: %w", err)
	}

	snapshotTemplateEnvID, err := o.resolveOrCreateSnapshotTemplate(ctx, sandboxID, teamID, upsertResult.BuildID, sbx.NodeID, opts)
	if err != nil {
		o.failSnapshotBuild(ctx, upsertResult.BuildID, err)

		return SnapshotTemplateResult{}, err
	}

	// Checkpoint pauses the sandbox, snapshots it, and resumes it on the
	// orchestrator with the same ExecutionID. Failed checkpoints include a
	// SandboxCheckpointFailure detail that tells the API whether the sandbox is
	// still running or should be removed from API-side state.
	client, childCtx := node.GetClient(ctx)
	_, err = client.Sandbox.Checkpoint(childCtx, &orchestrator.SandboxCheckpointRequest{
		SandboxId: sbx.SandboxID,
		BuildId:   upsertResult.BuildID.String(),
	})
	if err != nil {
		sandboxState, checkpointErr := checkpointFailureState(err)
		o.failSnapshotBuild(ctx, upsertResult.BuildID, checkpointErr)

		switch sandboxState {
		case orchestrator.SandboxCheckpointSandboxState_SANDBOX_CHECKPOINT_SANDBOX_STATE_RUNNING:
			finish(nil)
		default:
			finish(checkpointErr)
			o.removeCheckpointSandboxAPIState(ctx, sbx)
		}

		return SnapshotTemplateResult{}, checkpointErr
	}

	now := time.Now()
	err = o.sqlcDB.UpdateEnvBuildStatus(ctx, queries.UpdateEnvBuildStatusParams{
		Status:     types.BuildStatusUploaded,
		FinishedAt: &now,
		Reason:     types.BuildReason{},
		BuildID:    upsertResult.BuildID,
	})
	if err != nil {
		return SnapshotTemplateResult{}, fmt.Errorf("error updating build status: %w", err)
	}

	o.snapshotCache.Invalidate(context.WithoutCancel(ctx), sandboxID)

	telemetry.ReportEvent(ctx, "Snapshot template completed")

	return SnapshotTemplateResult{
		TemplateID: snapshotTemplateEnvID,
		BuildID:    upsertResult.BuildID,
	}, nil
}

func (o *Orchestrator) failSnapshotBuild(ctx context.Context, buildID uuid.UUID, cause error) {
	now := time.Now()
	err := o.sqlcDB.UpdateEnvBuildStatus(context.WithoutCancel(ctx), queries.UpdateEnvBuildStatusParams{
		Status:     types.BuildStatusFailed,
		FinishedAt: &now,
		Reason:     types.BuildReason{Message: cause.Error()},
		BuildID:    buildID,
	})
	if err != nil {
		telemetry.ReportError(ctx, "error failing build", err)
	}
}

func checkpointFailureState(err error) (orchestrator.SandboxCheckpointSandboxState, error) {
	checkpointErr := fmt.Errorf("checkpoint failed: %w", err)
	st, ok := grpcstatus.FromError(err)
	if ok {
		for _, detail := range st.Details() {
			failure, ok := detail.(*orchestrator.SandboxCheckpointFailure)
			if !ok {
				continue
			}

			message := failure.GetErrorMessage()
			if message == "" {
				message = fmt.Sprintf("checkpoint failed with sandbox state %s", failure.GetSandboxState())
			}

			return failure.GetSandboxState(), errors.New(message)
		}

		if st.Code() == codes.Canceled || st.Code() == codes.DeadlineExceeded {
			return orchestrator.SandboxCheckpointSandboxState_SANDBOX_CHECKPOINT_SANDBOX_STATE_RUNNING, checkpointErr
		}
	}

	if errors.Is(checkpointErr, context.Canceled) || errors.Is(checkpointErr, context.DeadlineExceeded) {
		return orchestrator.SandboxCheckpointSandboxState_SANDBOX_CHECKPOINT_SANDBOX_STATE_RUNNING, checkpointErr
	}

	return orchestrator.SandboxCheckpointSandboxState_SANDBOX_CHECKPOINT_SANDBOX_STATE_KILLED, checkpointErr
}

func (o *Orchestrator) removeCheckpointSandboxAPIState(ctx context.Context, sbx sandbox.Sandbox) {
	// The orchestrator reported the runtime sandbox as gone; only API-owned
	// state remains to be cleaned up here.
	cleanupCtx := context.WithoutCancel(ctx)
	if err := o.routingCatalog.DeleteSandbox(cleanupCtx, sbx.SandboxID, sbx.ExecutionID); err != nil {
		telemetry.ReportError(cleanupCtx, "error removing routing record after failed checkpoint", err)
	}

	o.sandboxStore.Remove(cleanupCtx, sbx.TeamID, sbx.SandboxID)
	go o.analyticsRemove(cleanupCtx, sbx, sandbox.StateActionKill)
}

func (o *Orchestrator) resolveOrCreateSnapshotTemplate(
	ctx context.Context,
	sandboxID string,
	teamID uuid.UUID,
	buildID uuid.UUID,
	originNodeID string,
	opts SnapshotTemplateOpts,
) (string, error) {
	// Existing template — just assign the build
	if opts.ExistingTemplateID != nil {
		err := o.sqlcDB.CreateTemplateBuildAssignment(ctx, queries.CreateTemplateBuildAssignmentParams{
			TemplateID: *opts.ExistingTemplateID,
			BuildID:    buildID,
			Tag:        opts.Tag,
		})
		if err != nil {
			return "", fmt.Errorf("error assigning build to existing template: %w", err)
		}

		return *opts.ExistingTemplateID, nil
	}

	// Create new snapshot template env
	envID, err := o.sqlcDB.CreateSnapshotTemplateEnv(ctx, queries.CreateSnapshotTemplateEnvParams{
		SnapshotID:   id.Generate(),
		TeamID:       teamID,
		SandboxID:    sandboxID,
		OriginNodeID: &originNodeID,
		BuildID:      &buildID,
		Tag:          opts.Tag,
	})
	if err != nil {
		return "", fmt.Errorf("error creating snapshot template env: %w", err)
	}

	// Create alias if a name was provided
	if opts.Alias != nil && opts.Namespace != nil {
		err = o.sqlcDB.CreateTemplateAlias(ctx, queries.CreateTemplateAliasParams{
			Alias:      *opts.Alias,
			TemplateID: envID,
			Namespace:  opts.Namespace,
		})
		if err != nil {
			return "", fmt.Errorf("error creating alias '%s': %w", *opts.Alias, err)
		}
	}

	return envID, nil
}
