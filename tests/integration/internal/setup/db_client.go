package setup

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
)

func GetTestDBClient(tb testing.TB) *db.DB {
	tb.Helper()

	database, err := db.NewClient(1, 1)
	require.NoError(tb, err)

	tb.Cleanup(func() {
		database.Close()
	})

	return database
}
