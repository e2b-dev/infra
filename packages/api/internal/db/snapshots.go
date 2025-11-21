package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
)

var ErrSnapshotNotFound = errors.New("no snapshot found")

type SnapshotWithBuilds struct {
	queries.Snapshot
	Builds []queries.EnvBuild
}

func GetSnapshotWithBuilds(ctx context.Context, db *sqlcdb.Client, teamID uuid.UUID, sandboxID string) (SnapshotWithBuilds, error) {
	snapshotWithBuilds, err := db.GetSnapshotBuilds(ctx, queries.GetSnapshotBuildsParams{
		SandboxID: sandboxID,
		TeamID:    teamID,
	})
	if err != nil {
		return SnapshotWithBuilds{}, fmt.Errorf("error getting snapshot with builds: %w", err)
	}

	if len(snapshotWithBuilds) == 0 {
		return SnapshotWithBuilds{}, ErrSnapshotNotFound
	}

	snapshot := SnapshotWithBuilds{Snapshot: snapshotWithBuilds[0].Snapshot, Builds: []queries.EnvBuild{}}
	for _, entry := range snapshotWithBuilds {
		if entry.EnvBuild.ID == uuid.Nil {
			continue
		}

		snapshot.Builds = append(snapshot.Builds, entry.EnvBuild)
	}

	return snapshot, nil
}
