package setup

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
)

func GetTestDBClient(tb testing.TB) *db.DB {
	tb.Helper()

	dbPool, err := db.NewPool(tb.Context())
	require.NoError(tb, err)
	tb.Cleanup(func() { dbPool.Close() })

	dbConn := db.Open(dbPool)

	database := db.NewClient(dbConn)
	require.NoError(tb, err)
	tb.Cleanup(func() { database.Close() })

	return database
}
