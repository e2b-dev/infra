package authdb

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// GetUserEmailsByUserIDs returns a map of userID → email by joining each user's
// default team. Used as a fallback when the Ory admin API is unavailable.
func (db *Client) GetUserEmailsByUserIDs(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	if len(userIDs) == 0 {
		return map[uuid.UUID]string{}, nil
	}

	const q = `
		SELECT ut.user_id, t.email
		FROM public.users_teams ut
		JOIN public.teams t ON t.id = ut.team_id
		WHERE ut.user_id = ANY($1::uuid[])
		  AND ut.is_default = true
	`

	rows, err := db.readConn.Query(ctx, q, userIDs)
	if err != nil {
		return nil, fmt.Errorf("query user emails from default teams: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]string, len(userIDs))
	for rows.Next() {
		var userID uuid.UUID
		var email string
		if err := rows.Scan(&userID, &email); err != nil {
			return nil, fmt.Errorf("scan user email row: %w", err)
		}
		result[userID] = email
	}
	return result, rows.Err()
}

// FindUserIDsByEmail returns all user IDs whose default-team email matches.
// Callers must handle the ambiguous case (len > 1) explicitly rather than
// silently picking one. Used as a fallback when the Ory admin API is unavailable.
func (db *Client) FindUserIDsByEmail(ctx context.Context, email string) ([]uuid.UUID, error) {
	if email == "" {
		return nil, nil
	}

	const q = `
		SELECT ut.user_id
		FROM public.users_teams ut
		JOIN public.teams t ON t.id = ut.team_id
		WHERE lower(t.email) = lower($1::text)
		  AND ut.is_default = true
	`

	rows, err := db.readConn.Query(ctx, q, email)
	if err != nil {
		return nil, fmt.Errorf("find user ids by email: %w", err)
	}
	defer rows.Close()

	var userIDs []uuid.UUID
	for rows.Next() {
		var userID uuid.UUID
		if scanErr := rows.Scan(&userID); scanErr != nil {
			return nil, fmt.Errorf("scan user id by email: %w", scanErr)
		}
		userIDs = append(userIDs, userID)
	}
	return userIDs, rows.Err()
}
