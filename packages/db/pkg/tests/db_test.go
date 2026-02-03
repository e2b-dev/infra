package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestRequireRowLevelSecurity(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)

	results, err := db.TestQueries.GetRowLevelSecurity(t.Context())
	require.NoError(t, err)
	assert.NotEmpty(t, results)

	var checked int
	for _, item := range results {
		if item.NamespaceName != "public" {
			// only the "public" namespace requires row level security
			continue
		}

		checked++
		assert.Truef(t, item.RowLevelSecurity, "row level security not enabled for %s.%s", item.NamespaceName, item.TableName)
	}
	assert.NotEmpty(t, checked)
}
