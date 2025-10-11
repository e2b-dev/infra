package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

var (
	tokenID        = uuid.MustParse("3d98c426-d348-446b-bdf6-5be3ca4123e2")
	userTokenValue = "89215020937a4c989cde33d7bc647715"
	teamTokenValue = "53ae1fed82754c17ad8077fbc8bcdd90"
	userID         = uuid.MustParse("fb69f46f-eb51-4a87-a14e-306f7a3fd89c")
)

func main() {
	ctx := context.Background()

	if err := run(ctx); err != nil {
		panic(err)
	}
}

func run(ctx context.Context) error {
	connectionString := os.Getenv("POSTGRES_CONNECTION_STRING")

	if connectionString == "" {
		if err := os.Setenv(
			"POSTGRES_CONNECTION_STRING",
			"postgresql://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
		); err != nil {
			return fmt.Errorf("failed to set environment variable: %w", err)
		}
	}

	// init database
	database, err := db.NewClient(1, 0)
	if err != nil {
		return fmt.Errorf("failed to initialize db: %w", err)
	}

	// create user
	user, err := upsertUser(ctx, database)
	if err != nil {
		return fmt.Errorf("failed to upsert user: %w", err)
	}

	// create team
	team, err := upsertTeam(ctx, database)
	if err != nil {
		return fmt.Errorf("failed to upsert team: %w", err)
	}

	if err = ensureUserIsOnTeam(ctx, database, user, team); err != nil {
		return fmt.Errorf("failed to ensure user is on team: %w", err)
	}

	// create user token
	if err = upsertUserToken(ctx, database, user, keys.AccessTokenPrefix, userTokenValue); err != nil {
		return fmt.Errorf("failed to upsert token: %w", err)
	}

	// create team token
	if err = upsertTeamToken(ctx, database, team, keys.ApiKeyPrefix, teamTokenValue); err != nil {
		return fmt.Errorf("failed to upsert token: %w", err)
	}

	// create local cluster
	// if err = upsertLocalCluster(ctx, database); err != nil {
	//	return fmt.Errorf("failed to upsert local cluster: %w", err)
	//}

	return nil
}

func upsertTeamToken(ctx context.Context, database *db.DB, team *models.Team, tokenPrefix, token string) error {
	tokenHash, tokenMask, err := createTokenHash(tokenPrefix, token)
	if err != nil {
		return fmt.Errorf("failed to create token hash: %w", err)
	}

	if _, err = database.Client.TeamAPIKey.Create().
		SetID(tokenID).
		SetName("local dev seed token").
		SetTeam(team).
		SetAPIKeyHash(tokenHash).
		SetAPIKeyLength(tokenMask.ValueLength).
		SetAPIKeyPrefix(tokenMask.Prefix).
		SetAPIKeyMaskPrefix(tokenMask.MaskedValuePrefix).
		SetAPIKeyMaskSuffix(tokenMask.MaskedValueSuffix).
		Save(ctx); ignoreConstraints(err) != nil {
		return fmt.Errorf("failed to create token: %w", err)
	}

	return nil
}

func ensureUserIsOnTeam(ctx context.Context, database *db.DB, user *models.User, team *models.Team) error {
	if _, err := database.Client.UsersTeams.Create().
		SetTeamID(team.ID).
		SetUserID(user.ID).
		Save(ctx); err != nil {
		if !models.IsConstraintError(err) {
			return fmt.Errorf("failed to add user to team: %w", err)
		}
	}

	return nil
}

func upsertUserToken(ctx context.Context, database *db.DB, user *models.User, tokenPrefix, token string) error {
	tokenHash, tokenMask, err := createTokenHash(tokenPrefix, token)
	if err != nil {
		return fmt.Errorf("failed to create token hash: %w", err)
	}

	if _, err = database.Client.AccessToken.Create().
		SetID(tokenID).
		SetUser(user).
		SetAccessTokenHash(tokenHash).
		SetAccessTokenLength(tokenMask.ValueLength).
		SetAccessTokenPrefix(tokenMask.Prefix).
		SetAccessTokenMaskPrefix(tokenMask.MaskedValuePrefix).
		SetAccessTokenMaskSuffix(tokenMask.MaskedValueSuffix).
		Save(ctx); ignoreConstraints(err) != nil {
		return fmt.Errorf("failed to create token: %w", err)
	}

	return nil
}

func ignoreConstraints(err error) error {
	var pqerr *pq.Error
	if errors.As(err, &pqerr) {
		if pqerr.Code == "23505" {
			return nil
		}
	}
	return err
}

func updateTeam[T interface {
	SetEmail(value string) T
	SetName(value string) T
	SetTier(value string) T
}](cmd T) T {
	return cmd.
		SetEmail("team@e2b-dev.local").
		SetName("local-dev team").
		SetTier("base_v1")
}

func upsertTeam(ctx context.Context, database *db.DB) (*models.Team, error) {
	teamID := uuid.MustParse("0b8a3ded-4489-4722-afd1-1d82e64ec2d5")
	team, err := database.Client.Team.Get(ctx, teamID)
	if err == nil {
		cmd := database.Client.Team.UpdateOne(team)
		cmd = updateTeam(cmd)
		team, err = cmd.Save(ctx)
	} else if models.IsNotFound(err) {
		cmd := database.Client.Team.Create()
		cmd = updateTeam(cmd).SetID(teamID)
		team, err = cmd.Save(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create team: %w", err)
	}
	return team, nil
}

func upsertUser(ctx context.Context, database *db.DB) (*models.User, error) {
	user, err := database.Client.User.Get(ctx, userID)
	if err == nil {
		cmd := database.Client.User.UpdateOne(user)
		cmd = updateUser(cmd)
		user, err = cmd.Save(ctx)
	} else if models.IsNotFound(err) {
		cmd := database.Client.User.Create()
		cmd = updateUser(cmd).SetID(userID)
		user, err = cmd.Save(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to upsert user: %w", err)
	}
	return user, nil
}

func updateUser[T interface {
	SetEmail(email string) T
	AddTeams(teams ...*models.Team) T
}](cmd T) T {
	return cmd.
		SetEmail("user@e2b-dev.local")
}

func createTokenHash(prefix, accessToken string) (string, keys.MaskedIdentifier, error) {
	hasher := keys.NewSHA256Hashing()
	tokenWithoutPrefix := strings.TrimPrefix(accessToken, prefix)
	accessTokenBytes, err := hex.DecodeString(tokenWithoutPrefix)
	if err != nil {
		return "", keys.MaskedIdentifier{}, fmt.Errorf("failed to hex decode string")
	}
	accessTokenHash := hasher.Hash(accessTokenBytes)
	accessTokenMask, err := keys.MaskKey(prefix, tokenWithoutPrefix)
	if err != nil {
		return "", keys.MaskedIdentifier{}, fmt.Errorf("failed to mask key")
	}
	return accessTokenHash, accessTokenMask, nil
}
