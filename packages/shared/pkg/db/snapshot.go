package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/snapshot"
)

type SnapshotInfo struct {
	SandboxID          string
	BaseTemplateID     string
	VCPU               int64
	RAMMB              int64
	Metadata           map[string]string
	TotalDiskSizeMB    int64
	KernelVersion      string
	FirecrackerVersion string
	EnvdVersion        string
}

// Check if there exists snapshot with the ID, if yes then return a new
// snapshot and env build. Otherwise create a new one.
func (db *DB) NewSnapshotBuild(
	ctx context.Context,
	snapshotConfig *SnapshotInfo,
	teamID uuid.UUID,
) (*models.EnvBuild, error) {
	tx, err := db.Client.BeginTx(ctx, nil)
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

func (db *DB) GetLastSnapshot(ctx context.Context, sandboxID string, teamID uuid.UUID) (
	*models.Snapshot,
	*models.EnvBuild,
	error,
) {

	snap, err := db.Client.Snapshot.Query().Where(snapshot.SandboxID(sandboxID)).Only(ctx)
	if err != nil {
		notFound := models.IsNotFound(err)

		if notFound {
			return nil, nil, SnapshotNotFound{}
		} else {
			return nil, nil, fmt.Errorf("failed to get snapshot for '%s': %w", sandboxID, err)
		}
	}

	build, err := db.Client.EnvBuild.Query().Where(envbuild.StatusEQ(envbuild.StatusSuccess), envbuild.EnvID(snap.EnvID)).Order(models.Desc(envbuild.FieldFinishedAt)).First(ctx)
	if err != nil {
		notFound := models.IsNotFound(err)

		if notFound {
			return snap, nil, BuildNotFound{}
		} else {
			return nil, nil, fmt.Errorf("failed to get build for '%s': %w", sandboxID, err)
		}
	}

	_, err = db.
		Client.
		Env.
		Query().
		Where(
			env.ID(snap.EnvID),
			env.TeamID(teamID),
		).Only(ctx)
	if err != nil {
		notFound := models.IsNotFound(err)

		if notFound {
			return nil, nil, TemplateNotFound{}
		} else {
			return nil, nil, fmt.Errorf("failed to get template for '%s': %w", sandboxID, err)
		}
	}

	return snap, build, nil
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
		WithSnapshots(func(query *models.SnapshotQuery) {
			query.Where(snapshot.SandboxID(sandboxID)).Only(ctx)
		}).
		WithBuilds().
		Only(ctx)

	notFound := models.IsNotFound(err)

	if err != nil {
		return nil, nil, fmt.Errorf("failed to get snapshot build for '%s': %w", sandboxID, err)
	}

	if notFound {
		return nil, nil, fmt.Errorf("no snapshot build found for '%s'", sandboxID)
	}

	return e, e.Edges.Builds, nil
}
