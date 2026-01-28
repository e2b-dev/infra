package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var ErrSandboxNotRunning = errors.New("sandbox is not in running state")

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
func (o *Orchestrator) CreateSnapshotTemplate(ctx context.Context, teamID uuid.UUID, sandboxID string, opts SnapshotTemplateOpts) (result SnapshotTemplateResult, e error) {
	ctx, span := tracer.Start(ctx, "create-snapshot-template")
	defer span.End()

	// Atomically transition to snapshotting state
	sbx, err := o.sandboxStore.Update(ctx, teamID, sandboxID, func(s sandbox.Sandbox) (sandbox.Sandbox, error) {
		if s.State != sandbox.StateRunning {
			return s, ErrSandboxNotRunning
		}
		s.State = sandbox.StateSnapshotting

		return s, nil
	})
	if err != nil {
		return SnapshotTemplateResult{}, fmt.Errorf("failed to start snapshotting: %w", err)
	}

	// Ensure we transition back to running when done (success or failure)
	defer func() {
		_, updateErr := o.sandboxStore.Update(ctx, sbx.TeamID, sbx.SandboxID, func(s sandbox.Sandbox) (sandbox.Sandbox, error) {
			if s.State == sandbox.StateSnapshotting {
				s.State = sandbox.StateRunning
			}

			return s, nil
		})
		e = errors.Join(e, updateErr)
	}()

	node := o.GetNode(sbx.ClusterID, sbx.NodeID)
	if node == nil {
		return SnapshotTemplateResult{}, fmt.Errorf("node '%s' not found", sbx.NodeID)
	}

	// Step 1: UpsertSnapshot - creates snapshot record, env, build, and build_assignment
	upsertResult, err := o.sqlcDB.UpsertSnapshot(ctx, buildUpsertSnapshotParams(sbx, node))
	if err != nil {
		return SnapshotTemplateResult{}, fmt.Errorf("error upserting snapshot: %w", err)
	}

	// Step 2: Resolve or create the snapshot template env
	snapshotTemplateEnvID, err := o.resolveOrCreateSnapshotTemplate(ctx, sandboxID, teamID, upsertResult.BuildID, opts)
	if err != nil {
		return SnapshotTemplateResult{}, err
	}

	// Step 3: Call the node's Checkpoint gRPC - this pauses and resumes atomically
	client, childCtx := node.GetClient(ctx)
	_, err = client.Sandbox.Checkpoint(childCtx, &orchestrator.SandboxCheckpointRequest{
		SandboxId: sbx.SandboxID,
		BuildId:   upsertResult.BuildID.String(),
	})
	if err != nil {
		return SnapshotTemplateResult{}, fmt.Errorf("checkpoint failed: %w", err)
	}

	// Step 4: Update the build status to uploaded
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

	telemetry.ReportEvent(ctx, "Snapshot template completed")

	return SnapshotTemplateResult{
		TemplateID: snapshotTemplateEnvID,
		BuildID:    upsertResult.BuildID,
	}, nil
}

func (o *Orchestrator) resolveOrCreateSnapshotTemplate(
	ctx context.Context,
	sandboxID string,
	teamID uuid.UUID,
	buildID uuid.UUID,
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
		SnapshotID: id.Generate(),
		TeamID:     teamID,
		SandboxID:  sandboxID,
		BuildID:    buildID,
		Tag:        opts.Tag,
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
