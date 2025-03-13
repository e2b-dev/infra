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

func (db *DB) GetEnvs(ctx context.Context, teamID uuid.UUID) (result []*Template, err error) {
	envs, err := db.
		Client.
		Env.
		Query().
		Where(
			env.TeamID(teamID),
			env.HasBuildsWith(envbuild.StatusEQ(envbuild.StatusUploaded)),
			env.Not(env.HasSnapshots()),
		).
		Order(models.Asc(env.FieldCreatedAt)).
		WithEnvAliases().
		WithCreator().
		WithBuilds(func(query *models.EnvBuildQuery) {
			query.Where(envbuild.StatusEQ(envbuild.StatusUploaded)).Order(models.Desc(envbuild.FieldFinishedAt))
		}).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list envs: %w", err)
	}

	for _, item := range envs {
		aliases := make([]string, len(item.Edges.EnvAliases))
		for i, alias := range item.Edges.EnvAliases {
			aliases[i] = alias.ID
		}

		var createdBy *TemplateCreator
		if item.Edges.Creator != nil {
			createdBy = &TemplateCreator{Id: item.Edges.Creator.ID, Email: item.Edges.Creator.Email}
		}

		build := item.Edges.Builds[0]
		result = append(result, &Template{
			TemplateID:    item.ID,
			TeamID:        item.TeamID,
			BuildID:       build.ID.String(),
			VCPU:          build.Vcpu,
			RAMMB:         build.RAMMB,
			DiskMB:        build.FreeDiskSizeMB,
			Public:        item.Public,
			Aliases:       &aliases,
			CreatedAt:     item.CreatedAt,
			UpdatedAt:     item.UpdatedAt,
			LastSpawnedAt: item.LastSpawnedAt,
			SpawnCount:    item.SpawnCount,
			BuildCount:    item.BuildCount,
			CreatedBy:     createdBy,
		})
	}

	return result, nil
}

func (db *DB) GetEnv(ctx context.Context, aliasOrEnvID string) (result *Template, build *models.EnvBuild, err error) {
	template, err := db.
		Client.
		Env.
		Query().
		Where(
			env.Or(
				env.HasEnvAliasesWith(envalias.ID(aliasOrEnvID)),
				env.ID(aliasOrEnvID),
			),
			env.HasBuildsWith(envbuild.StatusEQ(envbuild.StatusUploaded)),
		).
		WithEnvAliases(func(query *models.EnvAliasQuery) {
			query.Order(models.Asc(envalias.FieldID)) // TODO: remove once we have only 1 alias per env
		}).Only(ctx)

	notFound := models.IsNotFound(err)
	if notFound {
		return nil, nil, fmt.Errorf("template '%s' not found: %w", aliasOrEnvID, err)
	} else if err != nil {
		return nil, nil, fmt.Errorf("failed to get env '%s': %w", aliasOrEnvID, err)
	}

	build, err = db.Client.EnvBuild.Query().Where(envbuild.EnvID(template.ID), envbuild.StatusEQ(envbuild.StatusUploaded)).Order(models.Desc(envbuild.FieldFinishedAt)).Limit(1).Only(ctx)
	notFound = models.IsNotFound(err)
	if notFound {
		return nil, nil, fmt.Errorf("build for '%s' not found: %w", aliasOrEnvID, err)
	} else if err != nil {
		return nil, nil, fmt.Errorf("failed to get env '%s': %w", aliasOrEnvID, err)
	}

	aliases := make([]string, len(template.Edges.EnvAliases))
	for i, alias := range template.Edges.EnvAliases {
		aliases[i] = alias.ID
	}

	return &Template{
		TemplateID:    template.ID,
		BuildID:       build.ID.String(),
		VCPU:          build.Vcpu,
		RAMMB:         build.RAMMB,
		DiskMB:        build.FreeDiskSizeMB,
		Public:        template.Public,
		Aliases:       &aliases,
		TeamID:        template.TeamID,
		CreatedAt:     template.CreatedAt,
		UpdatedAt:     template.UpdatedAt,
		LastSpawnedAt: template.LastSpawnedAt,
		SpawnCount:    template.SpawnCount,
		BuildCount:    template.BuildCount,
	}, build, nil
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
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to finish env build '%s': %w", buildID, err)
	}

	return nil
}

func (db *DB) EnvBuildSetStatus(
	ctx context.Context,
	envID string,
	buildID uuid.UUID,
	status envbuild.Status,
) error {
	err := db.Client.EnvBuild.Update().Where(envbuild.ID(buildID), envbuild.EnvID(envID)).
		SetStatus(status).SetFinishedAt(time.Now()).Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to set env build status %s for '%s': %w", status, buildID, err)
	}

	return nil
}

func (db *DB) UpdateEnvLastUsed(ctx context.Context, count int64, time time.Time, envID string) (err error) {
	return db.Client.Env.UpdateOneID(envID).AddSpawnCount(count).SetLastSpawnedAt(time).Exec(ctx)
}
