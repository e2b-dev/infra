package utils

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
)

func CreateUser(t *testing.T, db *db.DB) uuid.UUID {
	t.Helper()

	userID := uuid.New()

	user, err := db.Client.User.Create().
		SetID(userID).
		SetEmail(fmt.Sprintf("user-test-integration-%s@e2b.dev", userID)).
		Save(t.Context())
	require.NoError(t, err)

	t.Cleanup(func() {
		db.Client.User.DeleteOneID(userID).Exec(t.Context())
	})

	return user.ID
}
