package management

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func (s *Service) PurgeUserAccessTokens(ctx context.Context, userID uuid.UUID) error {
	if err := s.db.PurgeUserAccessTokens(ctx, userID); err != nil {
		return fmt.Errorf("purge user access tokens: %w", err)
	}

	return nil
}
