package tests

import (
	"testing"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireRowLevelSecurity(t *testing.T) {
	db := testutils.SetupDatabase(t)

	results, err := db.TestQueries.GetRowLevelSecurity(t.Context())
	require.NoError(t, err)
	assert.NotEmpty(t, results)

	for _, item := range results {
		if item.NamespaceName == "pg_catalog" {
			// built ins don't need security
			continue
		}

		if item.NamespaceName == "information_schema" {
			// information schema doesn't have row level security
			continue
		}

		if item.NamespaceName == "auth" && item.TableName == "users" {
			// this table does not need row level security
			continue
		}

		assert.Truef(t, item.RowLevelSecurity, "row level security not enabled for %s.%s", item.NamespaceName, item.TableName)
	}
}
