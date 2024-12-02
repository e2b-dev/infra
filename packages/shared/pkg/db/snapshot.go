package db

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/snapshot"

	"github.com/google/uuid"
)

func (db *DB) GetSnapshotBuild(ctx context.Context, sandboxID, envID string, teamID uuid.UUID) (
	*models.Snapshot,
	*models.EnvBuild,
	error,
) {
	s, err := db.
		Client.
		Snapshot.
		Query().
		Where(
			snapshot.SandboxID(sandboxID),
			snapshot.HasEnvWith(env.TeamID(teamID)),
		).
		WithEnv().
		Only(ctx)

	notFound := models.IsNotFound(err)

	if err != nil && !notFound {
		return nil, nil, fmt.Errorf("failed to get snapshot '%s': %w", sandboxID, err)
	}

	if notFound {
		// s, err := db.
		// 	Client.
		// 	Snapshot.
		// 	Create().
		// 	SetEnv(env)
		// Only(ctx)
	}



	// s, err := db.
	// 	Client.
	// 	EnvBuild.
	// 	Create().
	// 	Set
		

	// dbEnv, err := db.
	// 	Client.
	// 	EnvBuild.
	// 	Create().

	// Check if there exists snapshot with the ID, if yes then return a new
}
