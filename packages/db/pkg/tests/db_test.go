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

		var isSecure bool
		if item.Kind == "v" {
			isSecure = isSecurityInvoker(item.Options)
		} else {
			isSecure = item.RowLevelSecurity
		}

		assert.Truef(t, isSecure, "database object %s.%s is not secure [%s] (%v)", item.NamespaceName, item.TableName, item.Kind, item.Options)
	}
	assert.NotEmpty(t, checked)
}

func isSecurityInvoker(options []string) bool {
	for _, option := range options {
		switch option {
		case "security_invoker=1", "security_invoker=true", "security_invoker=on":
			return true
		}
	}

	return false
}
