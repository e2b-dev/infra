package setup

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/client"
)

func GetTestDBClient(tb testing.TB) *client.Client {
	tb.Helper()

	db, err := client.NewClient(tb.Context())
	require.NoError(tb, err)

	tb.Cleanup(func() {
		db.Close()
	})

	return db
}
