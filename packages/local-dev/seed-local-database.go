package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	tokenID        = uuid.MustParse("3d98c426-d348-446b-bdf6-5be3ca4123e2")
	userTokenValue = "89215020937a4c989cde33d7bc647715"
	teamTokenValue = "53ae1fed82754c17ad8077fbc8bcdd90"
	clusterToken   = "6e15ed023bd545d88f71446f14b28661"
)

func main() {
	ctx := context.Background()

	if err := run(ctx); err != nil {
		panic(err)
	}
}

func run(ctx context.Context) error {
	if err := os.Setenv(
		"POSTGRES_CONNECTION_STRING",
		"postgresql://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
	); err != nil {
		return fmt.Errorf("failed to set environment variable: %w", err)
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

	err = ensureUserIsOnTeam(ctx, database, user, team)

	// create user token
	if err = upsertUserToken(ctx, database, user, keys.AccessTokenPrefix, userTokenValue); err != nil {
		return fmt.Errorf("failed to upsert token: %w", err)
	}

	// create team token
	if err = upsertTeamToken(ctx, database, team, keys.ApiKeyPrefix, teamTokenValue); err != nil {
		return fmt.Errorf("failed to upsert token: %w", err)
	}

	// create local cluster
	//if err = upsertLocalCluster(ctx, database); err != nil {
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
		return fmt.Errorf("failed to add user to team: %w", err)
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

type command[T any] interface {
	Save(ctx context.Context) (T, error)
}

type clusterCommand[T any] interface {
	SetEndpointTLS(b bool) T
	SetEndpoint(s string) T
	SetToken(t string) T
	SetSandboxProxyDomain(s string) T
}

func updateCluster[T clusterCommand[T]](cmd T) T {
	return cmd.
		SetEndpointTLS(false).
		SetToken(clusterToken).
		SetSandboxProxyDomain("e2b-dev.local").
		SetEndpoint("http://localhost:10666")
}

func upsertLocalCluster(ctx context.Context, database *db.DB) error {
	clusterID := uuid.MustParse("c8c92517-474f-411b-aa99-29139498bd51")
	clusters := database.Client.Cluster
	cluster, err := clusters.Get(ctx, clusterID)

	var cmd command[*models.Cluster]
	if err == nil {
		cmd = updateCluster(clusters.UpdateOne(cluster))
	} else if models.IsNotFound(err) {
		cmd = updateCluster(clusters.Create()).SetID(clusterID)
	} else {
		return fmt.Errorf("failed to get cluster: %w", err)
	}
	cluster, err = cmd.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to create team: %w", err)
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
	SetEmail(string) T
	SetName(string) T
	SetTier(string) T
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
	} else if errors.Is(err, sql.ErrNoRows) {
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
	userID := uuid.MustParse("fb69f46f-eb51-4a87-a14e-306f7a3fd89c")
	user, err := database.Client.User.Get(ctx, userID)
	if err == nil {
		cmd := database.Client.User.UpdateOne(user)
		cmd = updateUser(cmd)
		user, err = cmd.Save(ctx)
	} else if errors.Is(err, sql.ErrNoRows) {
		cmd := database.Client.User.Create()
		cmd = updateUser(cmd).SetID(userID)
		user, err = cmd.Save(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create team: %w", err)
	}
	return user, nil
}

func updateUser[T interface {
	SetEmail(email string) T
	AddTeams(...*models.Team) T
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
