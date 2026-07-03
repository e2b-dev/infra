package utils

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func CreateUser(t *testing.T, db *setup.Database) uuid.UUID {
	t.Helper()

	userID := uuid.New()

	err := db.AuthDb.TestsRawSQL(t.Context(), `
INSERT INTO auth.users (id, email)
VALUES ($1, $2)
`, userID, fmt.Sprintf("user-test-integration-%s@e2b.dev", userID))
	require.NoError(t, err)

	err = db.AuthDb.Write.UpsertPublicUser(t.Context(), userID)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = db.AuthDb.Write.DeletePublicUser(t.Context(), userID)
		db.AuthDb.TestsRawSQL(t.Context(), `
DELETE FROM auth.users WHERE id = $1
`, userID)
	})

	return userID
}
