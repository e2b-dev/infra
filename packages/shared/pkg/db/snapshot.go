package db

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/snapshot"
)

type SnapshotInfo struct {
	SandboxID          string
	SandboxStartedAt   time.Time
	BaseTemplateID     string
	VCPU               int64
	RAMMB              int64
	Metadata           map[string]string
	TotalDiskSizeMB    int64
	KernelVersion      string
	FirecrackerVersion string
	EnvdVersion        string
	EnvdSecured        bool
}

// Check if there exists snapshot with the ID, if yes then return a new
// snapshot and env build. Otherwise create a new one.
func (db *DB) NewSnapshotBuild(
	ctx context.Context,
	snapshotConfig *SnapshotInfo,
	teamID uuid.UUID,
) (*models.EnvBuild, error) {
	tx, err := db.Client.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	s, err := tx.
		Snapshot.
		Query().
		Where(
			snapshot.SandboxID(snapshotConfig.SandboxID),
			snapshot.HasEnvWith(env.TeamID(teamID)),
		).
		WithEnv().
		Only(ctx)

	notFound := models.IsNotFound(err)

	if err != nil && !notFound {
		return nil, fmt.Errorf("failed to get snapshot '%s': %w", snapshotConfig.SandboxID, err)
	}

	var e *models.Env

	if notFound {
		envID := id.Generate()

		e, err = tx.
			Env.
			Create().
			SetPublic(false).
			SetNillableCreatedBy(nil).
			SetTeamID(teamID).
			SetID(envID).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create env '%s': %w", snapshotConfig.SandboxID, err)
		}

		s, err = tx.
			Snapshot.
			Create().
			SetSandboxID(snapshotConfig.SandboxID).
			SetBaseEnvID(snapshotConfig.BaseTemplateID).
			SetEnv(e).
			SetMetadata(snapshotConfig.Metadata).
			SetSandboxStartedAt(snapshotConfig.SandboxStartedAt).
			SetEnvSecure(snapshotConfig.EnvdSecured).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create snapshot '%s': %w", snapshotConfig.SandboxID, err)
		}
	} else {
		e = s.Edges.Env
		// Update existing snapshot with new metadata and pause time
		s, err = tx.
			Snapshot.
			UpdateOne(s).
			SetMetadata(snapshotConfig.Metadata).
			SetSandboxStartedAt(snapshotConfig.SandboxStartedAt).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to update snapshot '%s': %w", snapshotConfig.SandboxID, err)
		}
	}

	b, err := tx.
		EnvBuild.
		Create().
		SetEnv(e).
		SetVcpu(snapshotConfig.VCPU).
		SetRAMMB(snapshotConfig.RAMMB).
		SetFreeDiskSizeMB(0).
		SetKernelVersion(snapshotConfig.KernelVersion).
		SetFirecrackerVersion(snapshotConfig.FirecrackerVersion).
		SetEnvdVersion(snapshotConfig.EnvdVersion).
		SetStatus(envbuild.StatusBuilding).
		SetTotalDiskSizeMB(snapshotConfig.TotalDiskSizeMB).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create env build '%s': %w", snapshotConfig.SandboxID, err)
	}

	err = tx.Commit()
	if err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return b, nil
}

func (db *DB) GetSnapshotBuilds(ctx context.Context, sandboxID string, teamID uuid.UUID) (
	*models.Env,
	[]*models.EnvBuild,
	error,
) {
	e, err := db.
		Client.
		Env.
		Query().
		Where(
			env.HasSnapshotsWith(snapshot.SandboxID(sandboxID)),
			env.TeamID(teamID),
		).
		WithBuilds().
		Only(ctx)

	notFound := models.IsNotFound(err)

	if notFound {
		return nil, nil, EnvNotFound{}
	}

	if err != nil {
		return nil, nil, fmt.Errorf("failed to get snapshot build for '%s': %w", sandboxID, err)
	}

	return e, e.Edges.Builds, nil
}

func (db *DB) GetTeamSnapshotsWithCursor(
	ctx context.Context,
	teamID uuid.UUID,
	excludeSandboxIDs []string,
	limit int,
	metadataFilter *map[string]string,
	cursorTime time.Time,
	cursorID string,
) (
	[]*models.Snapshot,
	error,
) {
	query := db.
		Client.
		Snapshot.
		Query().
		Where(
			snapshot.HasEnvWith(
				env.And(
					env.TeamID(teamID),
					env.HasBuildsWith(envbuild.StatusEQ(envbuild.StatusSuccess)),
				),
			),
		).
		WithEnv(func(query *models.EnvQuery) {
			query.WithBuilds(func(query *models.EnvBuildQuery) {
				query.Order(models.Desc(envbuild.FieldFinishedAt))
			})
		})

	// Apply cursor-based filtering if cursor is provided
	query = query.Where(
		snapshot.Or(
			snapshot.CreatedAtLT(cursorTime),
			snapshot.And(
				snapshot.CreatedAtEQ(cursorTime),
				snapshot.SandboxIDGT(cursorID),
			),
		),
	)

	// Apply metadata filtering
	if metadataFilter != nil {
		query = query.Where(snapshot.MetadataContains(*metadataFilter))
	}

	// Order by created_at (descending), then by sandbox_id (ascending) for stability
	query = query.Order(models.Desc(snapshot.FieldCreatedAt), models.Asc(snapshot.FieldSandboxID))

	// Apply limit + 1 to check if there are more results
	query = query.Limit(limit + 1)

	snapshots, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapshots for team '%s': %w", teamID, err)
	}

	// Remove snapshots with excludeSandboxIDs
	excludeMap := make(map[string]struct{}, len(excludeSandboxIDs))
	for _, id := range excludeSandboxIDs {
		excludeMap[id] = struct{}{}
	}

	filteredSnapshots := make([]*models.Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if _, excluded := excludeMap[snapshot.SandboxID]; !excluded {
			filteredSnapshots = append(filteredSnapshots, snapshot)
		}
	}

	return filteredSnapshots, nil
}
