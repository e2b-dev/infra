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
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type SnapshotTemplateResult struct {
	TemplateID string
	BuildID    uuid.UUID
	// Unchanged is true when the orchestrator's in-guest inspector
	// reported no recovery-relevant changes since the prior snapshot
	// and the result here points at that prior snapshot. See issue
	// e2b-dev/infra#2580.
	Unchanged bool
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
	// SkipIfUnchanged makes the orchestrator consult the in-guest
	// inspector and short-circuit to the prior snapshot if nothing
	// changed. Default false preserves the historical always-pause
	// behavior. See issue e2b-dev/infra#2580.
	SkipIfUnchanged bool
}

// CreateSnapshotTemplate creates a persistent snapshot template from a running sandbox and immediately resumes it.
// The handler is responsible for parsing the name, resolving aliases via the cache, and populating opts.
func (o *Orchestrator) CreateSnapshotTemplate(ctx context.Context, teamID uuid.UUID, sandboxID string, opts SnapshotTemplateOpts) (result SnapshotTemplateResult, e error) {
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
	// orchestrator with the same ExecutionID. On error the orchestrator
	// kills the sandbox itself; RemoveSandbox is still needed to clean up
	// API-side state (store, routing, analytics).
	client, childCtx := node.GetClient(ctx)
	checkpointResp, err := client.Sandbox.Checkpoint(childCtx, &orchestrator.SandboxCheckpointRequest{
		SandboxId:       sbx.SandboxID,
		BuildId:         upsertResult.BuildID.String(),
		SkipIfUnchanged: opts.SkipIfUnchanged,
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

		return SnapshotTemplateResult{}, fmt.Errorf("checkpoint failed: %w", err)
	}

	// Short-circuit handling: orchestrator skipped the pause because the
	// inspector reported no changes. The just-issued upsert is now
	// vestigial; mark it cancelled and look up the template owning the
	// prior build so we can return it to the caller.
	if checkpointResp.GetUnchanged() {
		priorBuild, parseErr := uuid.Parse(checkpointResp.GetPublishedBuildId())
		if parseErr == nil {
			priorTemplateID, lookupErr := o.sqlcDB.GetTemplateIDByBuildID(ctx, priorBuild)
			if lookupErr == nil {
				// Cancel the speculative new build — it represents zero
				// work and should not appear as a successful upload.
				o.failSnapshotBuild(ctx, upsertResult.BuildID, fmt.Errorf("skipped: no recovery-relevant changes"))
				o.snapshotCache.Invalidate(context.WithoutCancel(ctx), sandboxID)
				telemetry.ReportEvent(ctx, "Snapshot short-circuited (no changes)")
				return SnapshotTemplateResult{
					TemplateID: priorTemplateID,
					BuildID:    priorBuild,
					Unchanged:  true,
				}, nil
			}
			telemetry.ReportError(ctx, "skipIfUnchanged: prior build template lookup failed; falling through", lookupErr)
		}
		// If we can't resolve the prior template, fall through and
		// treat the new build as the canonical one. The orchestrator
		// already wrote the previous snapshot's artifacts, so the
		// new build_id is unbacked — but the existing failure path
		// below will mark it failed in that case.
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
	err := o.sqlcDB.UpdateEnvBuildStatus(ctx, queries.UpdateEnvBuildStatusParams{
		Status:     types.BuildStatusFailed,
		FinishedAt: sharedUtils.ToPtr(time.Now()),
		Reason:     types.BuildReason{Message: cause.Error()},
		BuildID:    buildID,
	})
	if err != nil {
		telemetry.ReportError(ctx, "error failing build", err)
	}
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
