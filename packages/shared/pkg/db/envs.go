package db

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envalias"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/snapshot"
	"github.com/e2b-dev/infra/packages/shared/pkg/schema"
)

type TemplateCreator struct {
	Email string
	Id    uuid.UUID
}

type Template struct {
	TemplateID    string
	BuildID       string
	TeamID        uuid.UUID
	VCPU          int64
	DiskMB        int64
	RAMMB         int64
	Public        bool
	Aliases       *[]string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastSpawnedAt time.Time
	SpawnCount    int64
	BuildCount    int32
	CreatedBy     *TemplateCreator
	EnvdVersion   string
}

type UpdateEnvInput struct {
	Public bool
}

func (db *DB) DeleteEnv(ctx context.Context, envID string) error {
	_, err := db.
		Client.
		Env.
		Delete().
		Where(env.ID(envID)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete env '%s': %w", envID, err)
	}

	return nil
}

func (db *DB) UpdateEnv(ctx context.Context, envID string, input UpdateEnvInput) error {
	return db.Client.Env.UpdateOneID(envID).SetPublic(input.Public).Exec(ctx)
}

func (db *DB) GetEnv(ctx context.Context, aliasOrEnvID string) (result *models.Env, err error) {
	template, err := db.
		Client.
		Env.
		Query().
		Where(
			env.Or(
				env.HasEnvAliasesWith(envalias.ID(aliasOrEnvID)),
				env.ID(aliasOrEnvID),
			),
		).
		WithEnvAliases(func(query *models.EnvAliasQuery) {
			query.Order(models.Asc(envalias.FieldID)) // TODO: remove once we have only 1 alias per env
		}).Only(ctx)

	notFound := models.IsNotFound(err)
	if notFound {
		return nil, TemplateNotFoundError{}
	} else if err != nil {
		return nil, fmt.Errorf("failed to get template '%s': %w", aliasOrEnvID, err)
	}

	return template, nil
}

func (db *DB) GetRunningEnvBuilds(ctx context.Context) ([]*models.EnvBuild, error) {
	envBuilds, err := db.
		Client.
		EnvBuild.
		Query().
		Where(envbuild.StatusIn(envbuild.StatusWaiting, envbuild.StatusBuilding)).
		Order(models.Desc(envbuild.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get running env builds: %w", err)
	}

	return envBuilds, nil
}

func (db *DB) GetEnvBuild(ctx context.Context, buildID uuid.UUID) (build *models.EnvBuild, err error) {
	dbBuild, err := db.
		Client.
		EnvBuild.
		Query().
		Where(envbuild.ID(buildID)).
		First(ctx)

	notFound := models.IsNotFound(err)
	if notFound {
		return nil, TemplateBuildNotFoundError{}
	} else if err != nil {
		return nil, fmt.Errorf("failed to get env build '%s': %w", buildID, err)
	}

	return dbBuild, nil
}

func (db *DB) CheckBaseEnvHasSnapshots(ctx context.Context, envID string) (result bool, err error) {
	result, err = db.Client.Snapshot.Query().Where(snapshot.BaseEnvID(envID)).Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to check if base env has snapshots for '%s': %w", envID, err)
	}

	return result, nil
}

func (db *DB) FinishEnvBuild(
	ctx context.Context,
	envID string,
	buildID uuid.UUID,
	totalDiskSizeMB int64,
	envdVersion string,
) error {
	err := db.Client.EnvBuild.Update().Where(envbuild.ID(buildID), envbuild.EnvID(envID)).
		SetFinishedAt(time.Now()).
		SetTotalDiskSizeMB(totalDiskSizeMB).
		SetStatus(envbuild.StatusUploaded).
		SetEnvdVersion(envdVersion).
		SetReason(nil).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to finish template build '%s': %w", buildID, err)
	}

	return nil
}

func (db *DB) EnvBuildSetStatus(
	ctx context.Context,
	envID string,
	buildID uuid.UUID,
	status envbuild.Status,
	reason *schema.BuildReason,
) error {
	err := db.Client.EnvBuild.Update().Where(envbuild.ID(buildID), envbuild.EnvID(envID)).
		SetStatus(status).
		SetFinishedAt(time.Now()).
		SetReason(reason).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to set template build status %s for '%s': %w", status, buildID, err)
	}

	return nil
}

func (db *DB) UpdateEnvLastUsed(ctx context.Context, count int64, time time.Time, envID string) (err error) {
	return db.Client.Env.UpdateOneID(envID).AddSpawnCount(count).SetLastSpawnedAt(time).Exec(ctx)
}
