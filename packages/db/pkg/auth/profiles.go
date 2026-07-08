package authdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// FindUserIDByEmail looks up the user whose default team email matches. Used as
// a fallback when the Ory admin API is unavailable for the add-member flow.
func (db *Client) FindUserIDByEmail(ctx context.Context, email string) (uuid.UUID, bool, error) {
	if email == "" {
		return uuid.Nil, false, nil
	}

	const q = `
		SELECT ut.user_id
		FROM public.users_teams ut
		JOIN public.teams t ON t.id = ut.team_id
		WHERE lower(t.email) = lower($1::text)
		  AND ut.is_default = true
		LIMIT 1
	`

	var userID uuid.UUID
	err := db.readConn.QueryRow(ctx, q, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("find user id by email: %w", err)
	}
	return userID, true, nil
}
