package setup

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Database struct {
	Db     *client.Client
	AuthDb *authdb.Client
}

// Shared DB client singleton - all tests share these pools
var (
	sharedDB     *Database
	sharedDBOnce sync.Once
	errSharedDB  error
)

func GetTestDBClient(tb testing.TB) *Database {
	tb.Helper()

	sharedDBOnce.Do(func() {
		databaseURL := utils.RequiredEnv("POSTGRES_CONNECTION_STRING", "Postgres connection string")

		// Shared pool across all tests - keep connection count low to avoid
		// exhausting Supabase session-mode pooler limits.
		// authdb creates 2 pools (write+read) so total = 1 + 2 = 3 connections.
		db, err := client.NewClient(context.Background(), databaseURL, pool.WithMaxConnections(1))
		if err != nil {
			errSharedDB = err

			return
		}

		authDb, err := authdb.NewClient(context.Background(), databaseURL, databaseURL, pool.WithMaxConnections(1))
		if err != nil {
			db.Close()
			errSharedDB = err

			return
		}

		sharedDB = &Database{
			Db:     db,
			AuthDb: authDb,
		}
	})

	require.NoError(tb, errSharedDB)

	return sharedDB
}
