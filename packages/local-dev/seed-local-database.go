package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

const (
	defaultOidcIssuer  = "http://localhost:4444/"
	defaultOidcSubject = "local-dev-user"
)

var (
	teamID         = uuid.MustParse("0b8a3ded-4489-4722-afd1-1d82e64ec2d5")
	teamTokenValue = "53ae1fed82754c17ad8077fbc8bcdd90"
	defaultUserID  = uuid.MustParse("fb69f46f-eb51-4a87-a14e-306f7a3fd89c")
)

func main() {
	ctx := context.Background()

	if err := run(ctx); err != nil {
		panic(err)
	}
}

func run(ctx context.Context) error {
	userID := defaultUserID
	if raw := strings.TrimSpace(os.Getenv("SEED_USER_ID")); raw != "" {
		var err error
		userID, err = uuid.Parse(raw)
		if err != nil || userID == uuid.Nil {
			return errors.New("SEED_USER_ID must be a nonzero UUID")
		}
	}

	connectionString := os.Getenv("POSTGRES_CONNECTION_STRING")

	if connectionString == "" {
		connectionString = "postgresql://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
	}

	oidcIssuer := os.Getenv("OIDC_ISSUER")
	if oidcIssuer == "" {
		oidcIssuer = defaultOidcIssuer
	}

	oidcSubject := os.Getenv("OIDC_SUBJECT")
	if oidcSubject == "" {
		oidcSubject = defaultOidcSubject
	}

	authDb, err := authdb.NewClient(ctx, connectionString)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer authDb.Close()

	// create user
	if err := upsertUser(ctx, authDb, userID); err != nil {
		return fmt.Errorf("failed to upsert user: %w", err)
	}

	// create team
	teamID, err := upsertTeam(ctx, authDb)
	if err != nil {
		return fmt.Errorf("failed to upsert team: %w", err)
	}

	if err = ensureUserIsOnTeam(ctx, authDb, userID, teamID); err != nil {
		return fmt.Errorf("failed to ensure user is on team: %w", err)
	}

	if err = upsertUserIdentity(ctx, authDb, userID, oidcIssuer, oidcSubject); err != nil {
		return fmt.Errorf("failed to upsert user identity: %w", err)
	}

	// create team token
	if err = upsertTeamAPIKey(ctx, authDb, userID, teamID, keys.ApiKeyPrefix, teamTokenValue); err != nil {
		return fmt.Errorf("failed to upsert token: %w", err)
	}

	// create local cluster
	// if err = upsertLocalCluster(ctx, db); err != nil {
	//	return fmt.Errorf("failed to upsert local cluster: %w", err)
	// }

	return nil
}

func upsertTeamAPIKey(ctx context.Context, db *authdb.Client, userID, teamID uuid.UUID, tokenPrefix, token string) error {
	tokenHash, tokenMask, err := createTokenHash(tokenPrefix, token)
	if err != nil {
		return fmt.Errorf("failed to create token hash: %w", err)
	}

	if _, err = db.CreateTeamAPIKey(ctx, authqueries.CreateTeamAPIKeyParams{
		TeamID:           teamID,
		CreatedBy:        &userID,
		ApiKeyHash:       tokenHash,
		ApiKeyPrefix:     tokenMask.Prefix,
		ApiKeyLength:     int32(tokenMask.ValueLength),
		ApiKeyMaskPrefix: tokenMask.MaskedValuePrefix,
		ApiKeyMaskSuffix: tokenMask.MaskedValueSuffix,
		Name:             "local dev seed token",
	}); ignoreConstraints(err) != nil {
		return fmt.Errorf("failed to create team api key: %w", err)
	}

	return nil
}

func upsertUserIdentity(ctx context.Context, db *authdb.Client, userID uuid.UUID, oidcIssuer, oidcSubject string) error {
	if _, err := db.UpsertPublicIdentity(ctx, authqueries.UpsertPublicIdentityParams{
		OidcIss: oidcIssuer,
		OidcSub: oidcSubject,
		UserID:  userID,
	}); err != nil {
		return fmt.Errorf("failed to upsert user identity: %w", err)
	}

	return nil
}

func ensureUserIsOnTeam(ctx context.Context, db *authdb.Client, userID, teamID uuid.UUID) error {
	if err := db.TestsRawSQL(ctx, `
INSERT INTO users_teams (user_id, team_id, is_default)
VALUES ($1, $2, NOT EXISTS (SELECT 1 FROM users_teams WHERE user_id = $1 AND is_default))
ON CONFLICT (team_id, user_id) DO UPDATE
SET is_default = users_teams.is_default OR EXCLUDED.is_default;`, userID, teamID); err != nil {
		return fmt.Errorf("failed to add user to team: %w", err)
	}

	return nil
}

func ignoreConstraints(err error) error {
	// sqlc check
	var pgconnErr *pgconn.PgError
	if errors.As(err, &pgconnErr) {
		if pgconnErr.Code == "23505" {
			return nil
		}
	}

	return err
}

func upsertTeam(ctx context.Context, db *authdb.Client) (uuid.UUID, error) {
	err := db.TestsRawSQL(ctx, `
INSERT INTO teams (id, email, name, tier, is_blocked, slug)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO UPDATE SET
	email = EXCLUDED.email,
	name = EXCLUDED.name,
	tier = EXCLUDED.tier,
	slug = EXCLUDED.slug
`, teamID, "team@e2b-dev.local", "local-dev team", "base_v1", false, "local-dev-team")
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to upsert team: %w", err)
	}

	return teamID, nil
}

func upsertUser(ctx context.Context, db *authdb.Client, userID uuid.UUID) error {
	err := db.TestsRawSQL(ctx, `
INSERT INTO auth.users (id, email)
VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET
	email = EXCLUDED.email
`, userID, "user@e2b-dev.local")
	if err != nil {
		return fmt.Errorf("failed to upsert user: %w", err)
	}

	err = db.UpsertPublicUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to upsert public user: %w", err)
	}

	return nil
}

func createTokenHash(prefix, accessToken string) (string, keys.MaskedIdentifier, error) {
	hasher := keys.NewSHA256Hashing()
	tokenWithoutPrefix := strings.TrimPrefix(accessToken, prefix)
	accessTokenBytes, err := hex.DecodeString(tokenWithoutPrefix)
	if err != nil {
		return "", keys.MaskedIdentifier{}, errors.New("failed to hex decode string")
	}
	accessTokenHash := hasher.Hash(accessTokenBytes)
	accessTokenMask, err := keys.MaskKey(prefix, tokenWithoutPrefix)
	if err != nil {
		return "", keys.MaskedIdentifier{}, errors.New("failed to mask key")
	}

	return accessTokenHash, accessTokenMask, nil
}
