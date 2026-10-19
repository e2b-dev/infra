package identity

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUnavailableServiceReportsNoIdentityProvider(t *testing.T) {
	t.Parallel()

	service := NewUnavailableService()
	ctx := t.Context()
	userID := uuid.New()

	_, err := service.ProfilesByUserID(ctx, []uuid.UUID{userID})
	require.ErrorIs(t, err, ErrNoIdentityProvider)
}
