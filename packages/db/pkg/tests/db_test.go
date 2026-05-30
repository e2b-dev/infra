package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestNoRowLevelSecurity(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)

	results, err := db.TestQueries.GetRowLevelSecurity(t.Context())
	require.NoError(t, err)
	assert.NotEmpty(t, results)

	var checked int
	for _, item := range results {
		if item.NamespaceName != "public" {
			// only the "public" namespace used to carry Supabase row level security.
			continue
		}
		if item.Kind == "v" {
			continue
		}

		checked++

		assert.Falsef(t, item.RowLevelSecurity, "database object %s.%s still has row level security enabled [%s]", item.NamespaceName, item.TableName, item.Kind)
	}
	assert.NotEmpty(t, checked)
}
