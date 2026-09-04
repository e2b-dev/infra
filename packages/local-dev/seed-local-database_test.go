package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestRun(t *testing.T) {
	postgresContainer, err := postgres.Run(t.Context(),
		"postgres:18-alpine",
		postgres.WithDatabase("test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("password"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := postgresContainer.Terminate(context.WithoutCancel(t.Context()))
		assert.NoError(t, err)
	})

	connectionString, err := postgresContainer.ConnectionString(t.Context(), "sslmode=disable")
	require.NoError(t, err)
	t.Setenv("POSTGRES_CONNECTION_STRING", connectionString)

	db, err := sql.Open("pgx", connectionString)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := db.Close()
		assert.NoError(t, err)
	})

	// run the db migration, through a provider carrying its own store rather
	// than goose's package-level dialect and tracking-table globals
	store, err := database.NewStore(goose.DialectPostgres, "_migrations")
	require.NoError(t, err)

	provider, err := goose.NewProvider(
		"", // Has to be empty when using a custom store
		db,
		os.DirFS(filepath.Join("..", "db", "migrations")),
		goose.WithStore(store),
	)
	require.NoError(t, err)

	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	canonicalUserID := uuid.New()
	t.Setenv("SEED_USER_ID", canonicalUserID.String())

	err = run(t.Context())
	require.NoError(t, err)
	var owner uuid.UUID
	err = db.QueryRowContext(t.Context(), "SELECT user_id FROM public.user_identities WHERE oidc_iss = $1 AND oidc_sub = $2", defaultOidcIssuer, defaultOidcSubject).Scan(&owner)
	require.NoError(t, err)
	require.Equal(t, canonicalUserID, owner)
	var member bool
	err = db.QueryRowContext(t.Context(), "SELECT EXISTS (SELECT 1 FROM public.users_teams WHERE user_id = $1 AND team_id = $2)", canonicalUserID, teamID).Scan(&member)
	require.NoError(t, err)
	require.True(t, member)

	var defaultTeam uuid.UUID
	require.NoError(t, run(t.Context()))
	err = db.QueryRowContext(t.Context(), "SELECT team_id FROM users_teams WHERE user_id=$1 AND is_default", canonicalUserID).Scan(&defaultTeam)
	require.NoError(t, err)
	require.Equal(t, teamID, defaultTeam)

	_, err = db.ExecContext(t.Context(), "UPDATE users_teams SET is_default=false WHERE user_id=$1", canonicalUserID)
	require.NoError(t, err)
	require.NoError(t, run(t.Context()))
	err = db.QueryRowContext(t.Context(), "SELECT team_id FROM users_teams WHERE user_id=$1 AND is_default", canonicalUserID).Scan(&defaultTeam)
	require.NoError(t, err)
	require.Equal(t, teamID, defaultTeam)

	existingTeam := uuid.New()
	_, err = db.ExecContext(t.Context(), "INSERT INTO teams (id,email,name,tier,is_blocked,slug) VALUES ($1,'test@example.com','Existing project','base_v1',false,$2)", existingTeam, existingTeam.String())
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "UPDATE users_teams SET is_default=false WHERE user_id=$1", canonicalUserID)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "INSERT INTO users_teams (user_id,team_id,is_default) VALUES ($1,$2,true)", canonicalUserID, existingTeam)
	require.NoError(t, err)
	require.NoError(t, run(t.Context()))
	err = db.QueryRowContext(t.Context(), "SELECT team_id FROM users_teams WHERE user_id=$1 AND is_default", canonicalUserID).Scan(&defaultTeam)
	require.NoError(t, err)
	require.Equal(t, existingTeam, defaultTeam)
}

func TestRunRejectsInvalidSeedUserID(t *testing.T) {
	t.Setenv("SEED_USER_ID", "not-a-uuid")
	require.ErrorContains(t, run(t.Context()), "SEED_USER_ID")
}
