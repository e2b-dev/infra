package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/snapshot"
)

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
		return nil, nil, EnvNotFoundError{}
	}

	if err != nil {
		return nil, nil, fmt.Errorf("failed to get snapshot build for '%s': %w", sandboxID, err)
	}

	return e, e.Edges.Builds, nil
}
