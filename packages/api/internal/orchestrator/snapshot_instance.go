package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var ErrSandboxNotRunning = errors.New("sandbox is not in running state")

type SnapshotResult struct {
	SnapshotID string
	BuildID    uuid.UUID
}

func buildUpsertSnapshotParams(sbx sandbox.Sandbox, node *nodemanager.Node) queries.UpsertSnapshotParams {
	machineInfo := node.MachineInfo()

	return queries.UpsertSnapshotParams{
		// Used if there's no snapshot for this sandbox yet
		TemplateID:     id.Generate(),
		TeamID:         sbx.TeamID,
		BaseTemplateID: sbx.BaseTemplateID,
		SandboxID:      sbx.SandboxID,
		StartedAt:      pgtype.Timestamptz{Time: sbx.StartTime, Valid: true},
		Vcpu:           sbx.VCpu,
		RamMb:          sbx.RamMB,
		// We don't know this information
		FreeDiskSizeMb:      0,
		TotalDiskSizeMb:     &sbx.TotalDiskSizeMB,
		Metadata:            sbx.Metadata,
		KernelVersion:       sbx.KernelVersion,
		FirecrackerVersion:  sbx.FirecrackerVersion,
		EnvdVersion:         &sbx.EnvdVersion,
		Secure:              sbx.EnvdAccessToken != nil,
		AllowInternetAccess: sbx.AllowInternetAccess,
		AutoPause:           sbx.AutoPause,
		Config: &types.PausedSandboxConfig{
			Version: types.PausedSandboxConfigVersion,
			Network: sbx.Network,
		},
		OriginNodeID:    node.ID,
		Status:          string(types.BuildStatusSnapshotting),
		CpuArchitecture: utils.ToPtr(machineInfo.CPUArchitecture),
		CpuFamily:       utils.ToPtr(machineInfo.CPUFamily),
		CpuModel:        utils.ToPtr(machineInfo.CPUModel),
		CpuModelName:    utils.ToPtr(machineInfo.CPUModelName),
		CpuFlags:        machineInfo.CPUFlags,
	}
}

// SnapshotSandbox creates a snapshot of a running sandbox and immediately resumes it.
// Returns the snapshotID and buildID. The sandbox remains running throughout.
func (o *Orchestrator) SnapshotSandbox(ctx context.Context, teamID uuid.UUID, sandboxID string) (result SnapshotResult, e error) {
	ctx, span := tracer.Start(ctx, "snapshot sandbox")
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
		return SnapshotResult{}, fmt.Errorf("failed to start snapshotting: %w", err)
	}

	// Ensure we transition back to running when done (success or failure)
	defer func() {
		_, updateErr := o.sandboxStore.Update(ctx, sbx.TeamID, sbx.SandboxID, func(s sandbox.Sandbox) (sandbox.Sandbox, error) {
			// Only transition back if still in snapshotting state
			if s.State == sandbox.StateSnapshotting {
				s.State = sandbox.StateRunning
			}

			return s, nil
		})
		e = errors.Join(e, updateErr)
	}()

	node := o.GetNode(sbx.ClusterID, sbx.NodeID)
	if node == nil {
		return SnapshotResult{}, fmt.Errorf("node '%s' not found", sbx.NodeID)
	}

	snapshotID := "snapshot_" + id.Generate()

	// Step 1: UpsertSnapshot - creates snapshot record, env, build, and build_assignment
	upsertResult, err := o.sqlcDB.UpsertSnapshot(ctx, buildUpsertSnapshotParams(sbx, node))
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("error upserting snapshot: %w", err)
	}

	// Step 2: Create the persistent snapshot env with source='snapshot' and link to the build
	snapshotEnvID, err := o.sqlcDB.CreateSnapshotEnv(ctx, queries.CreateSnapshotEnvParams{
		SnapshotID: snapshotID,
		TeamID:     teamID,
		SandboxID:  sandboxID,
		BuildID:    upsertResult.BuildID,
	})
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("error creating snapshot env: %w", err)
	}

	// Step 3: Call the node's Checkpoint gRPC - this pauses and resumes atomically
	client, childCtx := node.GetClient(ctx)
	_, err = client.Sandbox.Checkpoint(childCtx, &orchestrator.SandboxCheckpointRequest{
		SandboxId: sbx.SandboxID,
		BuildId:   upsertResult.BuildID.String(),
	})
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("checkpoint failed: %w", err)
	}

	// Step 4: Update the build status to uploaded
	now := time.Now()
	err = o.sqlcDB.UpdateEnvBuildStatus(ctx, queries.UpdateEnvBuildStatusParams{
		Status:     string(types.BuildStatusUploaded),
		FinishedAt: &now,
		Reason:     types.BuildReason{},
		BuildID:    upsertResult.BuildID,
	})
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("error updating build status: %w", err)
	}

	telemetry.ReportEvent(ctx, "Snapshot completed")

	return SnapshotResult{
		SnapshotID: snapshotEnvID,
		BuildID:    upsertResult.BuildID,
	}, nil
}
