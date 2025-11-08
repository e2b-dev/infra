package utils

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/client"
)

func CreateUser(t *testing.T, db *client.Client) uuid.UUID {
	t.Helper()

	userID := uuid.New()

	err := db.TestsRawSQL(t.Context(), `
INSERT INTO auth.users (id, email)
VALUES ($1, $2)
`, userID, fmt.Sprintf("user-test-integration-%s@e2b.dev", userID))
	require.NoError(t, err)

	t.Cleanup(func() {
		db.TestsRawSQL(t.Context(), `
DELETE FROM auth.users WHERE id = $1
`, userID)
	})

	return userID
}
