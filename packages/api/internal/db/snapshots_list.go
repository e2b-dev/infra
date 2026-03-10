package db

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
)

// SnapshotWithBuildAndAliases is the assembled result matching GetSnapshotsWithCursorRow.
type SnapshotWithBuildAndAliases struct {
	Aliases  []string
	Names    []string
	Snapshot queries.Snapshot
	EnvBuild queries.EnvBuild
}

// GetSnapshotsWithSplitQueries replaces the monolithic GetSnapshotsWithCursor query
// by splitting into 3 focused queries inside a single read-only repeatable-read
// transaction (consistent snapshot, no cross-query races):
//  1. Fetch base snapshots (with EXISTS check for ready builds)
//  2. Batch-fetch the latest ready build per env_id
//  3. Batch-fetch aliases per base_env_id
//
// Steps 2 and 3 run concurrently over the same transaction snapshot.
func GetSnapshotsWithSplitQueries(ctx context.Context, db *sqlcdb.Client, params queries.GetSnapshotsBaseParams) ([]SnapshotWithBuildAndAliases, error) {
	txClient, tx, err := db.WithReadOnlyTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting read-only transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	baseRows, err := txClient.GetSnapshotsBase(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("fetching base snapshots: %w", err)
	}

	if len(baseRows) == 0 {
		return nil, nil
	}

	envIDs := make([]string, 0, len(baseRows))
	baseEnvIDs := make([]string, 0, len(baseRows))
	envIDSet := make(map[string]struct{}, len(baseRows))
	baseEnvIDSet := make(map[string]struct{}, len(baseRows))

	for _, row := range baseRows {
		if _, ok := envIDSet[row.Snapshot.EnvID]; !ok {
			envIDs = append(envIDs, row.Snapshot.EnvID)
			envIDSet[row.Snapshot.EnvID] = struct{}{}
		}

		if _, ok := baseEnvIDSet[row.Snapshot.BaseEnvID]; !ok {
			baseEnvIDs = append(baseEnvIDs, row.Snapshot.BaseEnvID)
			baseEnvIDSet[row.Snapshot.BaseEnvID] = struct{}{}
		}
	}

	var (
		buildRows []queries.GetLatestReadyBuildsByEnvIDsRow
		aliasRows []queries.GetEnvAliasesByEnvIDsRow
	)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		var err error
		buildRows, err = txClient.GetLatestReadyBuildsByEnvIDs(gctx, envIDs)
		if err != nil {
			return fmt.Errorf("fetching builds: %w", err)
		}

		return nil
	})

	g.Go(func() error {
		var err error
		aliasRows, err = txClient.GetEnvAliasesByEnvIDs(gctx, baseEnvIDs)
		if err != nil {
			return fmt.Errorf("fetching aliases: %w", err)
		}

		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	buildByEnvID := make(map[string]queries.EnvBuild, len(buildRows))
	for _, row := range buildRows {
		buildByEnvID[row.LookupEnvID] = row.EnvBuild
	}

	type aliasInfo struct {
		aliases []string
		names   []string
	}

	aliasByEnvID := make(map[string]aliasInfo, len(aliasRows))
	for _, row := range aliasRows {
		aliasByEnvID[row.EnvID] = aliasInfo{aliases: row.Aliases, names: row.Names}
	}

	results := make([]SnapshotWithBuildAndAliases, 0, len(baseRows))

	for _, row := range baseRows {
		build := buildByEnvID[row.Snapshot.EnvID]

		var aliases []string
		var names []string
		if ai, ok := aliasByEnvID[row.Snapshot.BaseEnvID]; ok {
			aliases = ai.aliases
			names = ai.names
		}

		results = append(results, SnapshotWithBuildAndAliases{
			Aliases:  aliases,
			Names:    names,
			Snapshot: row.Snapshot,
			EnvBuild: build,
		})
	}

	return results, nil
}
