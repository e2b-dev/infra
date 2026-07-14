package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/storageopts"
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
	// FilesystemOnly makes the snapshot template persist only the filesystem:
	// its build is derived from the memory checkpoint (sharing its rootfs data)
	// and sandboxes created from it cold-boot. The source sandbox still gets a
	// full memory checkpoint and resumes with its memory intact.
	FilesystemOnly bool
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

	// The sandbox's own snapshot row is always a full memory snapshot, even for
	// a filesystem-only template: the sandbox is memory-resumed from it and it
	// can be memory-resumed again later (auto-resume included).
	upsertResult, err := o.throttledUpsertSnapshot(ctx, buildUpsertSnapshotParams(sbx, node, false))
	if err != nil {
		return SnapshotTemplateResult{}, fmt.Errorf("error upserting snapshot: %w", err)
	}
	buildIDs := []uuid.UUID{upsertResult.BuildID}

	// A filesystem-only template gets its own derived build that shares the
	// memory checkpoint's rootfs data via header references but carries no
	// memory snapshot, so sandboxes created from it cold-boot. Otherwise the
	// template reuses the memory build directly.
	templateBuildID := upsertResult.BuildID
	if opts.FilesystemOnly {
		templateBuildID, err = o.sqlcDB.CreateSnapshotTemplateBuild(ctx, buildSnapshotTemplateBuildParams(sbx, node))
		if err != nil {
			o.failSnapshotBuilds(ctx, buildIDs, err)

			return SnapshotTemplateResult{}, fmt.Errorf("error creating filesystem-only template build: %w", err)
		}
		buildIDs = append(buildIDs, templateBuildID)
	}

	snapshotTemplateEnvID, err := o.resolveOrCreateSnapshotTemplate(ctx, sandboxID, teamID, templateBuildID, sbx.NodeID, sbx.ClusterID, opts)
	if err != nil {
		o.failSnapshotBuilds(ctx, buildIDs, err)

		return SnapshotTemplateResult{}, err
	}

	// Checkpoint pauses the sandbox, snapshots it, and resumes it on the
	// orchestrator with the same ExecutionID. On error the orchestrator
	// kills the sandbox itself; RemoveSandbox is still needed to clean up
	// API-side state (store, routing, analytics).
	checkpointReq := &orchestrator.SandboxCheckpointRequest{
		SandboxId: sbx.SandboxID,
		BuildId:   upsertResult.BuildID.String(),
		Metadata:  map[string]string{storageopts.ObjectMetadataTemplateID: snapshotTemplateEnvID},
	}
	if opts.FilesystemOnly {
		// The memory build belongs to the sandbox's own snapshot env (like a
		// pause build); the derived build is the one owned by the template env.
		checkpointReq.Metadata = map[string]string{storageopts.ObjectMetadataTemplateID: upsertResult.TemplateID}
		checkpointReq.FilesystemBuildId = templateBuildID.String()
		checkpointReq.FilesystemMetadata = map[string]string{storageopts.ObjectMetadataTemplateID: snapshotTemplateEnvID}
	}

	client, childCtx := node.GetClient(ctx)
	_, err = client.Sandbox.Checkpoint(childCtx, checkpointReq)
	if err != nil {
		o.failSnapshotBuilds(ctx, buildIDs, err)

		// Complete the snapshotting transition with error — leaves state as
		// Snapshotting (no restore to Running) and clears the transition key
		// so RemoveSandbox can proceed without deadlock.
		finish(err)

		if killErr := o.RemoveSandbox(ctx, teamID, sandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill}); killErr != nil {
			telemetry.ReportError(ctx, "error killing sandbox after failed checkpoint", killErr)
		}

		return SnapshotTemplateResult{}, fmt.Errorf("checkpoint failed: %w", err)
	}

	now := time.Now()
	for _, buildID := range buildIDs {
		err = o.sqlcDB.UpdateEnvBuildStatus(ctx, queries.UpdateEnvBuildStatusParams{
			Status:     types.BuildStatusUploaded,
			FinishedAt: &now,
			Reason:     types.BuildReason{},
			BuildID:    buildID,
		})
		if err != nil {
			return SnapshotTemplateResult{}, fmt.Errorf("error updating build status: %w", err)
		}
	}

	o.snapshotCache.Invalidate(context.WithoutCancel(ctx), sandboxID)

	telemetry.ReportEvent(ctx, "Snapshot template completed")

	return SnapshotTemplateResult{
		TemplateID: snapshotTemplateEnvID,
		BuildID:    templateBuildID,
	}, nil
}

// buildSnapshotTemplateBuildParams mirrors buildUpsertSnapshotParams' build
// fields for the derived filesystem-only template build: same shape and CPU
// pinning to the source build, just not tied to the sandbox's snapshot env.
func buildSnapshotTemplateBuildParams(sbx sandbox.Sandbox, node *nodemanager.Node) queries.CreateSnapshotTemplateBuildParams {
	return queries.CreateSnapshotTemplateBuildParams{
		Vcpu:               sbx.VCpu,
		RamMb:              sbx.RamMB,
		FreeDiskSizeMb:     0,
		KernelVersion:      sbx.KernelVersion,
		FirecrackerVersion: sbx.FirecrackerVersion,
		EnvdVersion:        &sbx.EnvdVersion,
		Status:             types.BuildStatusSnapshotting,
		OriginNodeID:       &node.ID,
		TotalDiskSizeMb:    &sbx.TotalDiskSizeMB,
		SourceBuildID:      sbx.BuildID,
	}
}

func (o *Orchestrator) failSnapshotBuilds(ctx context.Context, buildIDs []uuid.UUID, cause error) {
	for _, buildID := range buildIDs {
		err := o.sqlcDB.UpdateEnvBuildStatus(ctx, queries.UpdateEnvBuildStatusParams{
			Status:     types.BuildStatusFailed,
			FinishedAt: new(time.Now()),
			Reason:     types.BuildReason{Message: cause.Error()},
			BuildID:    buildID,
		})
		if err != nil {
			telemetry.ReportError(ctx, "error failing build", err)
		}
	}
}

func (o *Orchestrator) resolveOrCreateSnapshotTemplate(
	ctx context.Context,
	sandboxID string,
	teamID uuid.UUID,
	buildID uuid.UUID,
	originNodeID string,
	clusterID uuid.UUID,
	opts SnapshotTemplateOpts,
) (string, error) {
	// Existing template — just assign the build
	if opts.ExistingTemplateID != nil {
		rows, err := o.sqlcDB.CreateTemplateBuildAssignment(ctx, queries.CreateTemplateBuildAssignmentParams{
			TemplateID: *opts.ExistingTemplateID,
			BuildID:    buildID,
			Tag:        opts.Tag,
		})
		if err != nil {
			return "", fmt.Errorf("error assigning build to existing template: %w", err)
		}
		if rows == 0 {
			return "", fmt.Errorf("template '%s' not found", *opts.ExistingTemplateID)
		}

		return *opts.ExistingTemplateID, nil
	}

	var clusterIDPtr *uuid.UUID
	if clusterID != consts.LocalClusterID {
		clusterIDPtr = &clusterID
	}

	// Create new snapshot template env
	envID, err := o.sqlcDB.CreateSnapshotTemplateEnv(ctx, queries.CreateSnapshotTemplateEnvParams{
		SnapshotID:   id.Generate(),
		TeamID:       teamID,
		ClusterID:    clusterIDPtr,
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
