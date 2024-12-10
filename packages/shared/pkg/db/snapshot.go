package db

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/snapshot"
	"go.uber.org/zap"

	"github.com/google/uuid"
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
	logger *zap.SugaredLogger,
) (*models.EnvBuild, error) {
	tx, err := db.Client.BeginTx(ctx, nil)
	defer tx.Rollback()

	logger.Infof("getting snapshot")

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
		logger.Infof("failed to get snapshot '%s': %v", snapshotConfig.SandboxID, err)

		return nil, fmt.Errorf("failed to get snapshot '%s': %w", snapshotConfig.SandboxID, err)
	}

	var e *models.Env

	if notFound {
		logger.Infof("creating env")
		envID := id.Generate()

		e, err = tx.
			Env.
			Create().
			SetPublic(false).
			SetTeamID(teamID).
			SetID(envID).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create env '%s': %w", snapshotConfig.SandboxID, err)
		}

		logger.Infof("creating snapshot")

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
	}

	logger.Infof("creating snapshot build")

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
	e, err := db.
		Client.
		Env.
		Query().
		Where(
			env.HasBuildsWith(envbuild.StatusEQ(envbuild.StatusSuccess)),
			env.HasSnapshotsWith(snapshot.SandboxID(sandboxID)),
		).
		WithSnapshots(func(query *models.SnapshotQuery) {
			query.Where(snapshot.SandboxID(sandboxID)).Only(ctx)
		}).
		WithBuilds(func(query *models.EnvBuildQuery) {
			query.Where(envbuild.StatusEQ(envbuild.StatusSuccess)).Order(models.Desc(envbuild.FieldFinishedAt)).Only(ctx)
		}).Only(ctx)

	notFound := models.IsNotFound(err)

	if err != nil && !notFound {
		return nil, nil, fmt.Errorf("failed to get snapshot build for '%s': %w", sandboxID, err)
	}

	return e.Edges.Snapshots[0], e.Edges.Builds[0], nil
}
